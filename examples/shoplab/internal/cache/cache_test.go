package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memKV is an in-memory KV with TTLs driven by a fake clock.
type memKV struct {
	mu   sync.Mutex
	m    map[string]memEntry
	now  func() time.Time
	ttls []time.Duration
	fail bool
}

type memEntry struct {
	v   []byte
	exp time.Time
}

func newMemKV(now func() time.Time) *memKV { return &memKV{m: map[string]memEntry{}, now: now} }

func (k *memKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail {
		return nil, false, errors.New("redis down")
	}
	e, ok := k.m[key]
	if !ok || !k.now().Before(e.exp) {
		return nil, false, nil
	}
	return e.v, true, nil
}

func (k *memKV) Set(_ context.Context, key string, v []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail {
		return errors.New("redis down")
	}
	k.m[key] = memEntry{v: v, exp: k.now().Add(ttl)}
	k.ttls = append(k.ttls, ttl)
	return nil
}

func (k *memKV) flush() { k.mu.Lock(); k.m = map[string]memEntry{}; k.mu.Unlock() }

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// slowLoader simulates the expensive review-summary query.
func slowLoader(calls *atomic.Int64, d time.Duration) Loader {
	return func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []byte(`{"id":1}`), nil
	}
}

func stampede(t *testing.T, c *Cache, n int, load Loader) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range n {
		wg.Go(func() {
			<-start
			v, _, err := c.Fetch(context.Background(), "product:1", load)
			if err != nil || string(v) != `{"id":1}` {
				t.Errorf("fetch = %q, %v", v, err)
			}
		})
	}
	close(start)
	wg.Wait()
}

func newCache(fixed bool) (*Cache, *memKV, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	kv := newMemKV(clk.Now)
	c := New(kv, Options{TTL: 10 * time.Second, Fixed: fixed})
	c.now = clk.Now
	return c, kv, clk
}

func TestNaiveCacheStampedes(t *testing.T) {
	c, _, _ := newCache(false)
	var calls atomic.Int64
	stampede(t, c, 50, slowLoader(&calls, 50*time.Millisecond))
	// Every concurrent miss recomputes the value.
	if got := calls.Load(); got < 45 {
		t.Fatalf("naive cache: %d loads for 50 concurrent misses, expected ~50", got)
	}
	t.Logf("naive: %d loads for 50 concurrent misses", calls.Load())
}

func TestFixedCacheCollapsesMisses(t *testing.T) {
	c, _, _ := newCache(true)
	var calls atomic.Int64
	stampede(t, c, 50, slowLoader(&calls, 50*time.Millisecond))
	if got := calls.Load(); got != 1 {
		t.Fatalf("fixed cache: %d loads for 50 concurrent misses, want 1", got)
	}
}

func TestNaiveTTLIsFixed(t *testing.T) {
	c, kv, _ := newCache(false)
	var calls atomic.Int64
	for i := range 20 {
		key := "product:" + string(rune('a'+i))
		if _, _, err := c.Fetch(context.Background(), key, slowLoader(&calls, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ttl := range kv.ttls {
		if ttl != 10*time.Second {
			t.Fatalf("naive ttl = %v, want exactly 10s for every key", ttl)
		}
	}
}

func TestFixedTTLIsJittered(t *testing.T) {
	c, _, _ := newCache(true)
	seen := map[time.Duration]bool{}
	for range 200 {
		d := c.jitteredTTL()
		if d < 8*time.Second || d > 12*time.Second {
			t.Fatalf("jittered ttl %v outside ±20%%", d)
		}
		seen[d] = true
	}
	if len(seen) < 50 {
		t.Fatalf("only %d distinct TTLs; jitter not applied", len(seen))
	}
}

func TestFixedServesStaleAndRefreshesOnce(t *testing.T) {
	c, _, clk := newCache(true)
	var calls atomic.Int64
	load := slowLoader(&calls, 20*time.Millisecond)

	if _, st, _ := c.Fetch(context.Background(), "product:1", load); st != Miss {
		t.Fatalf("first fetch status = %s", st)
	}
	if _, st, _ := c.Fetch(context.Background(), "product:1", load); st != Hit {
		t.Fatalf("second fetch status = %s", st)
	}
	clk.Advance(15 * time.Second) // past freshness (≤12s), within stale window

	var wg sync.WaitGroup
	var stale atomic.Int64
	for range 50 {
		wg.Go(func() {
			begin := time.Now()
			_, st, err := c.Fetch(context.Background(), "product:1", load)
			if err != nil {
				t.Error(err)
			}
			if st == Stale {
				stale.Add(1)
			}
			if time.Since(begin) > 15*time.Millisecond {
				t.Errorf("stale read waited %v for the refresh", time.Since(begin))
			}
		})
	}
	wg.Wait()
	if stale.Load() != 50 {
		t.Fatalf("%d/50 served stale", stale.Load())
	}
	// Wait for the background refresh.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 2 {
		t.Fatalf("loads = %d, want 2 (initial + one background refresh)", got)
	}
	if _, st, _ := c.Fetch(context.Background(), "product:1", load); st != Hit {
		t.Fatalf("after refresh status = %s, want hit", st)
	}
}

func TestFixedSharedLoadSurvivesLeaderCancel(t *testing.T) {
	c, _, _ := newCache(true)
	var calls atomic.Int64
	load := slowLoader(&calls, 50*time.Millisecond)

	leaderCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, _, err := c.Fetch(leaderCtx, "product:1", load)
		leaderDone <- err
	}()
	time.Sleep(10 * time.Millisecond)
	follower := make(chan error, 1)
	go func() {
		_, _, err := c.Fetch(context.Background(), "product:1", load)
		follower <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader err = %v, want context.Canceled", err)
	}
	if err := <-follower; err != nil {
		t.Fatalf("follower failed because the leader hung up: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("loads = %d", calls.Load())
	}
}

func TestKVFailureFallsBackToLoad(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		c, kv, _ := newCache(fixed)
		kv.fail = true
		var calls atomic.Int64
		v, st, err := c.Fetch(context.Background(), "product:1", slowLoader(&calls, 0))
		if err != nil || string(v) != `{"id":1}` || st != Bypass {
			t.Fatalf("fixed=%v: got %q %s %v", fixed, v, st, err)
		}
	}
}

func TestLoaderErrorPropagates(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		c, _, _ := newCache(fixed)
		boom := errors.New("boom")
		_, _, err := c.Fetch(context.Background(), "k", func(context.Context) ([]byte, error) { return nil, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("fixed=%v: err = %v", fixed, err)
		}
	}
}

func TestModeSwitchReadsForeignEntriesAsMiss(t *testing.T) {
	naive, kv, clk := newCache(false)
	var calls atomic.Int64
	load := slowLoader(&calls, 0)
	if _, _, err := naive.Fetch(context.Background(), "product:1", load); err != nil {
		t.Fatal(err)
	}
	fixed := New(kv, Options{TTL: 10 * time.Second, Fixed: true})
	fixed.now = clk.Now
	v, st, err := fixed.Fetch(context.Background(), "product:1", load)
	if err != nil || st != Miss || string(v) != `{"id":1}` {
		t.Fatalf("got %q %s %v", v, st, err)
	}
}

func TestEncodeDecode(t *testing.T) {
	at := time.Unix(1_800_000_000, 123)
	b := encode(at, []byte("payload"))
	got, p, err := decode(b)
	if err != nil || !got.Equal(at) || string(p) != "payload" {
		t.Fatalf("decode = %v %q %v", got, p, err)
	}
	if _, _, err := decode([]byte("{}")); err == nil {
		t.Fatal("raw JSON must not decode as an envelope")
	}
}

func TestFlushThenStampede(t *testing.T) {
	// After a flush every key is cold at once; the fixed cache still loads
	// each key only once.
	c, kv, _ := newCache(true)
	var calls atomic.Int64
	load := slowLoader(&calls, 30*time.Millisecond)
	stampede(t, c, 20, load)
	kv.flush()
	stampede(t, c, 20, load)
	if calls.Load() != 2 {
		t.Fatalf("loads = %d, want 2", calls.Load())
	}
}
