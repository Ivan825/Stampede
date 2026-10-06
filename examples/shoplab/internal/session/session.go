// Package session is ShopLab's server-side, in-memory session store.
//
// Planted bottleneck (SHOPLAB_FIX_LEAK unset): expired sessions are rejected
// on lookup but never removed from the map, and every session pins a 16 KiB
// "response buffer" allocated at login. A soak test that logs in repeatedly
// therefore grows the heap without bound.
//
// Fixed: a janitor goroutine evicts expired sessions periodically, lookups
// delete expired entries eagerly, and no per-session buffer is allocated.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// LeakyBufferSize is the per-session buffer retained in leaky mode.
const LeakyBufferSize = 16 << 10

// approxOverhead is a rough per-session size for the map entry and struct.
const approxOverhead = 256

// Session is one logged-in browser/client.
type Session struct {
	ID        string // stable id used as the cart key; never sent to clients
	Token     string // bearer token, 32 random bytes hex-encoded
	UserID    int64
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time

	// buf is the leaky-mode per-session buffer ("preallocated so rendering
	// never allocates"); it is filled at login so the memory is really
	// resident, not just reserved.
	buf []byte
}

// Store is a concurrency-safe in-memory session store.
type Store struct {
	ttl   time.Duration
	fixed bool
	now   func() time.Time

	mu    sync.RWMutex
	m     map[string]*Session
	bytes atomic.Int64
}

// New creates a store. fixed selects the SHOPLAB_FIX_LEAK behaviour.
func New(ttl time.Duration, fixed bool) *Store {
	return &Store{ttl: ttl, fixed: fixed, now: time.Now, m: make(map[string]*Session)}
}

// SetClock overrides the time source (tests only).
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Create starts a new session for the user.
func (s *Store) Create(userID int64, email string) (*Session, error) {
	tok, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{
		ID: id, Token: tok, UserID: userID, Email: email,
		CreatedAt: now, ExpiresAt: now.Add(s.ttl),
	}
	size := int64(approxOverhead + len(tok) + len(id) + len(email))
	if !s.fixed {
		sess.buf = make([]byte, LeakyBufferSize)
		for i := range sess.buf {
			sess.buf[i] = byte(i)
		}
		size += LeakyBufferSize
	}
	s.mu.Lock()
	s.m[tok] = sess
	s.mu.Unlock()
	s.bytes.Add(size)
	return sess, nil
}

// Get returns the live session for token. Expired sessions are never
// returned; in fixed mode they are also removed.
func (s *Store) Get(token string) (*Session, bool) {
	s.mu.RLock()
	sess, ok := s.m[token]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if s.now().After(sess.ExpiresAt) {
		if s.fixed {
			s.delete(token)
		}
		return nil, false
	}
	return sess, true
}

// Delete removes a session (logout).
func (s *Store) Delete(token string) { s.delete(token) }

func (s *Store) delete(token string) {
	s.mu.Lock()
	sess, ok := s.m[token]
	if ok {
		delete(s.m, token)
	}
	s.mu.Unlock()
	if ok {
		s.bytes.Add(-sizeOf(sess))
	}
}

func sizeOf(sess *Session) int64 {
	return int64(approxOverhead + len(sess.Token) + len(sess.ID) + len(sess.Email) + len(sess.buf))
}

// Len is the number of sessions held, including expired ones not yet evicted.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// Bytes is the approximate memory retained by the store.
func (s *Store) Bytes() int64 { return s.bytes.Load() }

// Sweep removes every expired session and returns how many it removed.
func (s *Store) Sweep() int {
	now := s.now()
	var freed int64
	n := 0
	s.mu.Lock()
	for tok, sess := range s.m {
		if now.After(sess.ExpiresAt) {
			delete(s.m, tok)
			freed += sizeOf(sess)
			n++
		}
	}
	s.mu.Unlock()
	s.bytes.Add(-freed)
	return n
}

// RunJanitor sweeps every interval until ctx is done. It is a no-op in leaky
// mode, which is exactly the bug.
func (s *Store) RunJanitor(ctx context.Context, every time.Duration) {
	if !s.fixed {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
		}
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
