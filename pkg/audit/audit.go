// Copyright 2026 Sakura Tunnel Authors
// SPDX-License-Identifier: Apache-2.0

// Package audit writes an append-only JSONL audit trail for security relevant
// events: logins, tunnel registration, tenant changes.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one audit record.
type Event struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	User   string `json:"user,omitempty"`
	IP     string `json:"ip,omitempty"`
	Detail string `json:"detail,omitempty"`
	OK     bool   `json:"ok"`
}

// Logger appends events to a file. A nil *Logger is a no-op.
type Logger struct {
	mu   sync.Mutex
	path string
}

// Open creates the logger; an empty path disables auditing.
func Open(path string) (*Logger, error) {
	if path == "" {
		return nil, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Logger{path: path}, nil
}

// Path returns the backing file.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Write records one event. Errors are swallowed on purpose: auditing must never
// break the tunnel.
func (l *Logger) Write(e Event) {
	if l == nil {
		return
	}
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

// Tail returns the last n events, newest last.
func (l *Logger) Tail(n int) []Event {
	if l == nil || n <= 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := os.ReadFile(l.path)
	if err != nil {
		return nil
	}
	lines := splitLines(b)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]Event, 0, len(lines))
	for _, ln := range lines {
		if len(ln) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(ln, &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
