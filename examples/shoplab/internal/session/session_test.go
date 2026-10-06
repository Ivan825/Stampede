package session

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newStore(fixed bool) (*Store, *clock) {
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := New(time.Minute, fixed)
	s.SetClock(c.now)
	return s, c
}

func TestTokenFormat(t *testing.T) {
	s, _ := newStore(true)
	sess, err := s.Create(1, "a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Token) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars (32 bytes)", len(sess.Token))
	}
	other, _ := s.Create(1, "a@b.c")
	if other.Token == sess.Token || other.ID == sess.ID {
		t.Fatal("tokens/ids must be unique")
	}
}

func TestGetRejectsExpiredInBothModes(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		s, c := newStore(fixed)
		sess, _ := s.Create(42, "x@y.z")
		if got, ok := s.Get(sess.Token); !ok || got.UserID != 42 {
			t.Fatalf("fixed=%v: fresh session not found", fixed)
		}
		c.advance(2 * time.Minute)
		if _, ok := s.Get(sess.Token); ok {
			t.Fatalf("fixed=%v: expired session returned", fixed)
		}
		if _, ok := s.Get("nope"); ok {
			t.Fatalf("fixed=%v: unknown token accepted", fixed)
		}
	}
}

func TestLeakyStoreNeverShrinks(t *testing.T) {
	s, c := newStore(false)
	for range 100 {
		sess, _ := s.Create(1, "u@shoplab.test")
		if len(sess.buf) != LeakyBufferSize {
			t.Fatalf("leaky session buffer = %d bytes", len(sess.buf))
		}
	}
	c.advance(time.Hour)
	// Lookups of expired sessions do not evict them...
	if s.Len() != 100 {
		t.Fatalf("len = %d", s.Len())
	}
	// ...and the janitor does nothing in leaky mode.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunJanitor(ctx, time.Millisecond); close(done) }()
	select {
	case <-done: // returns immediately
	case <-time.After(time.Second):
		t.Fatal("leaky janitor should return immediately")
	}
	cancel()
	if s.Len() != 100 {
		t.Fatalf("len after janitor = %d", s.Len())
	}
	if s.Bytes() < 100*LeakyBufferSize {
		t.Fatalf("bytes = %d, want >= %d", s.Bytes(), 100*LeakyBufferSize)
	}
}

func TestFixedStoreEvicts(t *testing.T) {
	s, c := newStore(true)
	var tokens []string
	for range 100 {
		sess, _ := s.Create(1, "u@shoplab.test")
		if sess.buf != nil {
			t.Fatal("fixed sessions must not allocate a buffer")
		}
		tokens = append(tokens, sess.Token)
	}
	c.advance(30 * time.Second)
	fresh, _ := s.Create(2, "fresh@shoplab.test")
	c.advance(45 * time.Second) // first 100 expired, fresh one still valid

	// Lazy eviction on lookup.
	s.Get(tokens[0])
	if s.Len() != 100 {
		t.Fatalf("len after lazy eviction = %d, want 100", s.Len())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunJanitor(ctx, time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for s.Len() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Len() != 1 {
		t.Fatalf("len after janitor = %d, want 1", s.Len())
	}
	if _, ok := s.Get(fresh.Token); !ok {
		t.Fatal("unexpired session was evicted")
	}
	if b := s.Bytes(); b <= 0 || b > 1024 {
		t.Fatalf("bytes = %d, want a single small session", b)
	}
}

func TestDelete(t *testing.T) {
	s, _ := newStore(true)
	sess, _ := s.Create(1, "a@b.c")
	s.Delete(sess.Token)
	if _, ok := s.Get(sess.Token); ok || s.Len() != 0 || s.Bytes() != 0 {
		t.Fatalf("delete failed: len=%d bytes=%d", s.Len(), s.Bytes())
	}
}

// TestLeakIsRealHeapGrowth shows the leak is genuine retained heap, not just
// a counter: 2,000 expired leaky sessions keep ~32 MiB alive, while the fixed
// store releases everything once the janitor has swept.
func TestLeakIsRealHeapGrowth(t *testing.T) {
	heap := func() int64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return int64(ms.HeapAlloc)
	}
	const n = 2000
	for _, fixed := range []bool{false, true} {
		base := heap()
		s, c := newStore(fixed)
		for range n {
			if _, err := s.Create(1, "soak@shoplab.test"); err != nil {
				t.Fatal(err)
			}
		}
		c.advance(time.Hour) // every session is now expired
		if fixed {
			s.Sweep() // one janitor tick
		}
		grown := heap() - base
		runtime.KeepAlive(s)
		t.Logf("fixed=%v retained heap growth after expiry: %d KiB", fixed, grown/1024)
		if !fixed && grown < n*LeakyBufferSize*9/10 {
			t.Errorf("leaky store retained only %d bytes, want ~%d", grown, n*LeakyBufferSize)
		}
		if fixed && grown > 1<<20 {
			t.Errorf("fixed store retained %d bytes after sweep", grown)
		}
	}
}
