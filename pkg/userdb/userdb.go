// Copyright 2026 Sakura Tunnel Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package userdb implements the multi-tenant user store used by the tunnel
// server. Each record carries its own access token plus quota / bandwidth /
// proxy-count limits, and the live traffic counters that back the per-user
// traffic view in the dashboard.
//
// The store is a single JSON file so that it needs no external dependency and
// keeps the server binary small. Writes are atomic (temp file + rename).
package userdb

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrExists     = errors.New("user already exists")
	ErrBadName    = errors.New("invalid user name")
	ErrDisabled   = errors.New("user is disabled")
	ErrExpired    = errors.New("user has expired")
	ErrQuota      = errors.New("traffic quota exceeded")
	ErrBadToken   = errors.New("invalid token")
	ErrMaxProxies = errors.New("proxy count limit reached")
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,32}$`)

// User is one tenant of the tunnel server.
type User struct {
	Name               string `json:"user"`
	Token              string `json:"token"`
	Enabled            bool   `json:"enabled"`
	MaxProxies         int    `json:"maxProxies"`
	BandwidthLimitKbps int    `json:"bandwidthLimitKbps"`
	TrafficQuotaBytes  int64  `json:"trafficQuotaBytes"`
	CreatedAt          int64  `json:"createdAt"`
	ExpireAt           int64  `json:"expireAt"`
	LastSeenAt         int64  `json:"lastSeenAt"`
	Note               string   `json:"note"`
	AllowIPs           []string `json:"allowIPs,omitempty"`

	QuotaMonthly bool  `json:"quotaMonthly,omitempty"`
	LastResetAt  int64 `json:"lastResetAt,omitempty"`

	TrafficIn  int64 `json:"trafficIn"`
	TrafficOut int64 `json:"trafficOut"`

	// runtime only, never persisted
	activeProxies int
	onlineClients int
}

// ActiveProxies returns the number of proxies currently registered for the user.
func (u *User) ActiveProxies() int { return u.activeProxies }

// OnlineClients returns the number of clients currently connected for the user.
func (u *User) OnlineClients() int { return u.onlineClients }

// TotalTraffic returns the accumulated in+out byte count of the user.
func (u *User) TotalTraffic() int64 { return u.TrafficIn + u.TrafficOut }

// Expired reports whether the account passed its expiry timestamp.
func (u *User) Expired(now time.Time) bool {
	return u.ExpireAt > 0 && now.Unix() >= u.ExpireAt
}

// Check returns an error when the account may not be used right now.
func (u *User) Check(now time.Time) error {
	if !u.Enabled {
		return ErrDisabled
	}
	if u.Expired(now) {
		return ErrExpired
	}
	if u.TrafficQuotaBytes > 0 && u.TotalTraffic() >= u.TrafficQuotaBytes {
		return ErrQuota
	}
	return nil
}

type fileFormat struct {
	Version int     `json:"version"`
	Users   []*User `json:"users"`
}

// Store is a concurrency safe, JSON backed user store.
type Store struct {
	mu    sync.RWMutex
	path  string
	users map[string]*User
	dirty bool
}

// Open loads (or creates) the store at path. A missing file is not an error:
// the store starts empty and is written on the first mutation.
func Open(path string) (*Store, error) {
	s := &Store{
		path:  path,
		users: make(map[string]*User),
	}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("read user file: %w", err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var ff fileFormat
	if err := json.Unmarshal(b, &ff); err != nil {
		return nil, fmt.Errorf("parse user file %s: %w", path, err)
	}
	for _, u := range ff.Users {
		if u == nil || u.Name == "" {
			continue
		}
		if u.Token == "" {
			u.Token = NewToken()
		}
		s.users[u.Name] = u
	}
	return s, nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// NewToken returns a fresh, URL safe access token.
func NewToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand never fails on supported platforms; keep a usable fallback.
		return "sk_" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "sk_" + hex.EncodeToString(b[:])
}

// AuthKey derives the login key exactly like the tunnel protocol does.
func AuthKey(token string, timestamp int64) string {
	h := md5.New()
	h.Write([]byte(token))
	h.Write([]byte(strconv.FormatInt(timestamp, 10)))
	return hex.EncodeToString(h.Sum(nil))
}

// Save writes the store atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		s.dirty = false
		return nil
	}
	ff := fileFormat{Version: 1, Users: make([]*User, 0, len(s.users))}
	for _, u := range s.users {
		cp := *u
		ff.Users = append(ff.Users, &cp)
	}
	sort.Slice(ff.Users, func(i, j int) bool { return ff.Users[i].Name < ff.Users[j].Name })

	b, err := json.MarshalIndent(ff, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.dirty = false
	return nil
}

// AutoSave periodically flushes pending changes until stop is closed.
func (s *Store) AutoSave(stop <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			pending := s.dirty
			s.mu.Unlock()
			if pending {
				_ = s.Save()
			}
		case <-stop:
			return
		}
	}
}

// Get returns a copy of the user record.
func (s *Store) Get(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[name]
	if !ok {
		return User{}, false
	}
	cp := *u
	return cp, true
}

// Exists reports whether the user exists.
func (s *Store) Exists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.users[name]
	return ok
}

// Count returns the number of users.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// List returns copies of every user record sorted by name.
func (s *Store) List() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Create adds a new user and returns it together with its token.
func (s *Store) Create(u User) (User, error) {
	if !nameRe.MatchString(u.Name) {
		return User{}, ErrBadName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.Name]; ok {
		return User{}, ErrExists
	}
	if u.Token == "" {
		u.Token = NewToken()
	}
	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}
	u.Enabled = true
	stored := u
	s.users[u.Name] = &stored
	s.dirty = true
	if err := s.saveLocked(); err != nil {
		delete(s.users, u.Name)
		return User{}, err
	}
	return stored, nil
}

// Patch is the mutable part of a user record.
type Patch struct {
	Enabled            *bool
	MaxProxies         *int
	BandwidthLimitKbps *int
	TrafficQuotaBytes  *int64
	ExpireAt           *int64
	Note               *string
	AllowIPs           *[]string
	QuotaMonthly       *bool
}

// Update applies a patch to an existing user.
func (s *Store) Update(name string, p Patch) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return User{}, ErrNotFound
	}
	if p.Enabled != nil {
		u.Enabled = *p.Enabled
	}
	if p.MaxProxies != nil {
		v := *p.MaxProxies
		if v < 0 {
			v = 0
		}
		u.MaxProxies = v
	}
	if p.BandwidthLimitKbps != nil {
		v := *p.BandwidthLimitKbps
		if v < 0 {
			v = 0
		}
		u.BandwidthLimitKbps = v
	}
	if p.TrafficQuotaBytes != nil {
		v := *p.TrafficQuotaBytes
		if v < 0 {
			v = 0
		}
		u.TrafficQuotaBytes = v
	}
	if p.ExpireAt != nil {
		u.ExpireAt = *p.ExpireAt
	}
	if p.Note != nil {
		u.Note = *p.Note
	}
	if p.AllowIPs != nil {
		u.AllowIPs = *p.AllowIPs
	}
	if p.QuotaMonthly != nil {
		u.QuotaMonthly = *p.QuotaMonthly
		u.LastResetAt = time.Now().Unix()
	}
	s.dirty = true
	if err := s.saveLocked(); err != nil {
		return User{}, err
	}
	return *u, nil
}

// Delete removes a user.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[name]; !ok {
		return ErrNotFound
	}
	u := s.users[name]
	delete(s.users, name)
	s.dirty = true
	if err := s.saveLocked(); err != nil {
		s.users[name] = u
		return err
	}
	return nil
}

// ResetToken generates a new token for the user.
func (s *Store) ResetToken(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return "", ErrNotFound
	}
	u.Token = NewToken()
	s.dirty = true
	if err := s.saveLocked(); err != nil {
		return "", err
	}
	return u.Token, nil
}

// ResetTraffic clears the accumulated traffic counters of the user.
func (s *Store) ResetTraffic(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return ErrNotFound
	}
	u.TrafficIn = 0
	u.TrafficOut = 0
	s.dirty = true
	return s.saveLocked()
}

// Authenticate validates the login key of a user and returns the record.
func (s *Store) Authenticate(name, key string, timestamp int64, now time.Time) (User, error) {
	s.mu.RLock()
	u, ok := s.users[name]
	if !ok {
		s.mu.RUnlock()
		return User{}, ErrNotFound
	}
	token := u.Token
	cp := *u
	s.mu.RUnlock()

	if err := cp.Check(now); err != nil {
		return User{}, err
	}
	expected := AuthKey(token, timestamp)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(key)) != 1 {
		return User{}, ErrBadToken
	}
	return cp, nil
}

// AuthenticateAny validates a key against every enabled user. It is used for
// heartbeat / new-work-connection scopes, where the protocol carries no user.
func (s *Store) AuthenticateAny(key string, timestamp int64, now time.Time) error {
	s.mu.RLock()
	candidates := make([]string, 0, len(s.users))
	for _, u := range s.users {
		if u.Check(now) != nil {
			continue
		}
		candidates = append(candidates, u.Token)
	}
	s.mu.RUnlock()

	for _, token := range candidates {
		if subtle.ConstantTimeCompare([]byte(AuthKey(token, timestamp)), []byte(key)) == 1 {
			return nil
		}
	}
	return ErrBadToken
}

// AllByToken returns the user owning the given token.
func (s *Store) AllByToken(token string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.users {
		if subtle.ConstantTimeCompare([]byte(u.Token), []byte(token)) == 1 {
			return *u, true
		}
	}
	return User{}, false
}

// Touch updates the last-seen timestamp of a user.
func (s *Store) Touch(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[name]; ok {
		u.LastSeenAt = time.Now().Unix()
		s.dirty = true
	}
}

// AddTraffic accumulates traffic counters for a user.
func (s *Store) AddTraffic(name string, in, out int64) {
	if name == "" || (in == 0 && out == 0) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return
	}
	now := time.Now()
	if u.QuotaMonthly && monthChanged(u.LastResetAt, now) {
		u.TrafficIn = 0
		u.TrafficOut = 0
		u.LastResetAt = now.Unix()
	}
	u.TrafficIn += in
	u.TrafficOut += out
	s.dirty = true
}

// AddOnline adjusts the online client counter of a user.
func (s *Store) AddOnline(name string, delta int) {
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[name]; ok {
		u.onlineClients += delta
		if u.onlineClients < 0 {
			u.onlineClients = 0
		}
	}
}

// AddProxy adjusts the active proxy counter of a user.
func (s *Store) AddProxy(name string, delta int) {
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[name]; ok {
		u.activeProxies += delta
		if u.activeProxies < 0 {
			u.activeProxies = 0
		}
	}
}

// AllowProxy checks whether the user may register one more proxy.
func (s *Store) AllowProxy(name string, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[name]
	if !ok {
		return ErrNotFound
	}
	if err := u.Check(now); err != nil {
		return err
	}
	if u.MaxProxies > 0 && u.activeProxies >= u.MaxProxies {
		return ErrMaxProxies
	}
	return nil
}

// BandwidthBytes returns the user's server side bandwidth limit in bytes/s.
func (s *Store) BandwidthBytes(name string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[name]
	if !ok || u.BandwidthLimitKbps <= 0 {
		return 0
	}
	return int64(u.BandwidthLimitKbps) * 1024 / 8
}

// Snapshot returns a copy of every record including runtime counters.
func (s *Store) Snapshot() map[string]User {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]User, len(s.users))
	for k, v := range s.users {
		out[k] = *v
	}
	return out
}

// monthChanged reports whether the calendar month differs (monthly quota reset).
func monthChanged(last int64, now time.Time) bool {
	if last == 0 {
		return false
	}
	t := time.Unix(last, 0)
	y1, m1, _ := t.Date()
	y2, m2, _ := now.Date()
	return y1 != y2 || m1 != m2
}