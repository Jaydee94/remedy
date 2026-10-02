// Package auth implements the single-admin login: password check, in-memory sessions and a
// login rate limit. Sessions do not survive a restart, which is acceptable for one admin.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	SessionTTL     = 24 * time.Hour
	maxFailures    = 5
	failureWindow  = 10 * time.Minute
	argonTime      = 1
	argonMemoryKiB = 64 * 1024
	argonThreads   = 4
	argonKeyLen    = 32
)

type Auth struct {
	salt []byte
	hash []byte

	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]time.Time // token -> expiry
	failures map[string][]time.Time
}

// New derives an Argon2id hash of password. The plaintext is not kept.
func New(password string) *Auth {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	return &Auth{
		salt:     salt,
		hash:     derive(password, salt),
		now:      time.Now,
		sessions: map[string]time.Time{},
		failures: map[string][]time.Time{},
	}
}

func derive(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
}

// SetClock replaces the time source (tests only).
func (a *Auth) SetClock(now func() time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = now
}

func (a *Auth) Verify(password string) bool {
	return subtle.ConstantTimeCompare(derive(password, a.salt), a.hash) == 1
}

func (a *Auth) NewSession() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := base64.RawURLEncoding.EncodeToString(b)

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for t, exp := range a.sessions { // opportunistic cleanup
		if now.After(exp) {
			delete(a.sessions, t)
		}
	}
	a.sessions[tok] = now.Add(SessionTTL)
	return tok
}

func (a *Auth) ValidSession(token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[token]
	if !ok {
		return false
	}
	if a.now().After(exp) {
		delete(a.sessions, token)
		return false
	}
	return true
}

func (a *Auth) EndSession(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
}

// Locked reports whether key has hit the failure limit within the window.
func (a *Auth) Locked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recent(key)) >= maxFailures
}

func (a *Auth) RecordFailure(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[key] = append(a.recent(key), a.now())
}

func (a *Auth) ResetFailures(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.failures, key)
}

// recent drops failures outside the window and returns the rest. Callers hold a.mu.
func (a *Auth) recent(key string) []time.Time {
	cutoff := a.now().Add(-failureWindow)
	kept := a.failures[key][:0]
	for _, t := range a.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	a.failures[key] = kept
	return kept
}
