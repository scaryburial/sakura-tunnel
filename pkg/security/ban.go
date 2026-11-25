// Copyright 2026 Sakura Tunnel Authors
// SPDX-License-Identifier: Apache-2.0

// Package security implements the small built-in protections: brute force
// banning, IP allow lists and audit logging helpers.
package security

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// BanManager counts authentication failures per IP and temporarily blocks an IP
// that keeps failing. A nil manager allows everything.
type BanManager struct {
	mu        sync.Mutex
	entries   map[string]*banEntry
	threshold int
	banFor    time.Duration
	window    time.Duration
	now       func() time.Time
}

type banEntry struct {
	count   int
	firstAt time.Time
	until   time.Time
}

// NewBanManager returns a manager. threshold<=0 disables banning.
func NewBanManager(threshold int, banFor, window time.Duration) *BanManager {
	if threshold <= 0 {
		return nil
	}
	if banFor <= 0 {
		banFor = 10 * time.Minute
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &BanManager{
		entries:   make(map[string]*banEntry),
		threshold: threshold,
		banFor:    banFor,
		window:    window,
		now:       time.Now,
	}
}

// Allow reports whether the IP may attempt authentication right now.
func (b *BanManager) Allow(ip string) error {
	if b == nil || ip == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[ip]
	if !ok {
		return nil
	}
	now := b.now()
	if now.Before(e.until) {
		left := e.until.Sub(now).Round(time.Second)
		return fmt.Errorf("该 IP 因多次认证失败被临时封禁，剩余 %s", left)
	}
	if now.Sub(e.firstAt) > b.window {
		delete(b.entries, ip)
	}
	return nil
}

// Fail records one authentication failure and bans when the threshold is hit.
func (b *BanManager) Fail(ip string) {
	if b == nil || ip == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	e, ok := b.entries[ip]
	if !ok || now.Sub(e.firstAt) > b.window {
		e = &banEntry{count: 0, firstAt: now}
		b.entries[ip] = e
	}
	e.count++
	if e.count >= b.threshold {
		e.until = now.Add(b.banFor)
		e.count = 0
		e.firstAt = now
	}
}

// Reset clears the failure counter after a successful authentication.
func (b *BanManager) Reset(ip string) {
	if b == nil || ip == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, ip)
}

// Banned returns the number of currently banned IPs (for the dashboard).
func (b *BanManager) Banned() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	n := 0
	for _, e := range b.entries {
		if now.Before(e.until) {
			n++
		}
	}
	return n
}

// IPAllowed checks an IP against an allow list of plain IPs and CIDR blocks.
// An empty list allows everything.
func IPAllowed(ip string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, rule := range allow {
		rule = strings.TrimSpace(rule)
		if rule == "" || rule == "*" {
			return true
		}
		if strings.Contains(rule, "/") {
			if _, cidr, err := net.ParseCIDR(rule); err == nil && cidr.Contains(parsed) {
				return true
			}
			continue
		}
		if other := net.ParseIP(rule); other != nil && other.Equal(parsed) {
			return true
		}
	}
	return false
}
