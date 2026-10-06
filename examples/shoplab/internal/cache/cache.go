// Package cache is the read-through cache in front of the expensive product
// detail.
//
// Planted bottleneck (SHOPLAB_FIX_CACHE unset): a plain cache-aside with a
// fixed TTL. Keys written together (e.g. right after a flush or a cold start)
// expire together, and on a miss every concurrent request recomputes the
// value, so a hot key's expiry sends a burst of identical expensive queries
// to Postgres.
//
// Fixed: per-key singleflight collapses concurrent misses into one load, the
// TTL is jittered so keys do not expire in lockstep, and entries are kept
// past their freshness deadline so stale values are served instantly while a
// single background refresh runs (stale-while-revalidate).
package cache

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
)

// KV is the byte store the cache sits on (Redis in production).
type KV interface {
	// Get returns ok=false on a miss.
	Get(ctx context.Context, key string) (val []byte, ok bool, err error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// Status reports how a Fetch was served.
type Status string

// Fetch outcomes.
const (
	Hit    Status = "hit"
	Miss   Status = "miss"
	Stale  Status = "stale"
	Bypass Status = "error" // cache unavailable; loaded directly
)

// Loader computes a value from the source of truth.
type Loader func(ctx context.Context) ([]byte, error)

// Observer receives cache events (wired to Prometheus by the caller).
type Observer interface {
	Request(Status)
	Load(time.Duration)
}

type nopObserver struct{}

func (nopObserver) Request(Status)     {}
func (nopObserver) Load(time.Duration) {}

// Options configures a Cache.
type Options struct {
	TTL      time.Duration // freshness period
	Fixed    bool          // SHOPLAB_FIX_CACHE
	Jitter   float64       // fixed mode: TTL varies by ±Jitter (fraction), default 0.2
	StaleFor time.Duration // fixed mode: how long past freshness a value may be served, default 6×TTL
	// LoadTimeout bounds a shared (singleflight) or background load, which
	// must not be tied to any single request's context. Default 10s.
	LoadTimeout time.Duration
	Observer    Observer
	Logger      *slog.Logger
}

// Cache is a read-through cache over a KV.
type Cache struct {
	kv   KV
	opts Options
	sf   singleflight.Group
	now  func() time.Time
	rand func() float64
}

// New creates a cache.
func New(kv KV, opts Options) *Cache {
	if opts.Jitter <= 0 {
		opts.Jitter = 0.2
	}
	if opts.StaleFor <= 0 {
		opts.StaleFor = 6 * opts.TTL
	}
	if opts.LoadTimeout <= 0 {
		opts.LoadTimeout = 10 * time.Second
	}
	if opts.Observer == nil {
		opts.Observer = nopObserver{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Cache{kv: kv, opts: opts, now: time.Now, rand: rand.Float64}
}

// Fetch returns the value for key, loading it with load on a miss.
func (c *Cache) Fetch(ctx context.Context, key string, load Loader) ([]byte, Status, error) {
	if c.opts.Fixed {
		return c.fetchFixed(ctx, key, load)
	}
	return c.fetchNaive(ctx, key, load)
}

// fetchNaive is the planted bottleneck: classic cache-aside, fixed TTL, no
// coordination between concurrent misses.
func (c *Cache) fetchNaive(ctx context.Context, key string, load Loader) ([]byte, Status, error) {
	b, ok, err := c.kv.Get(ctx, key)
	if err == nil && ok {
		c.opts.Observer.Request(Hit)
		return b, Hit, nil
	}
	status := Miss
	if err != nil {
		status = Bypass
		c.opts.Logger.Warn("cache get failed", "key", key, "err", err)
	}
	c.opts.Observer.Request(status)
	v, err := c.timedLoad(ctx, load)
	if err != nil {
		return nil, status, err
	}
	if err := c.kv.Set(ctx, key, v, c.opts.TTL); err != nil {
		c.opts.Logger.Warn("cache set failed", "key", key, "err", err)
	}
	return v, status, nil
}

func (c *Cache) fetchFixed(ctx context.Context, key string, load Loader) ([]byte, Status, error) {
	b, ok, err := c.kv.Get(ctx, key)
	if err != nil {
		c.opts.Logger.Warn("cache get failed", "key", key, "err", err)
		c.opts.Observer.Request(Bypass)
		// Still collapse concurrent loads so a Redis outage does not
		// become a database stampede.
		v, err := c.shared(ctx, key, load, false)
		return v, Bypass, err
	}
	if ok {
		freshUntil, payload, decErr := decode(b)
		if decErr == nil {
			if c.now().Before(freshUntil) {
				c.opts.Observer.Request(Hit)
				return payload, Hit, nil
			}
			c.opts.Observer.Request(Stale)
			c.refreshInBackground(key, load)
			return payload, Stale, nil
		}
		// Unreadable entry (e.g. written by the naive mode): treat as miss.
	}
	c.opts.Observer.Request(Miss)
	v, err := c.shared(ctx, key, load, true)
	return v, Miss, err
}

// shared runs load at most once per key across concurrent callers. The load
// runs on a context detached from the caller so that one client hanging up
// does not fail every request waiting on the same key.
func (c *Cache) shared(ctx context.Context, key string, load Loader, store bool) ([]byte, error) {
	ch := c.sf.DoChan(key, func() (any, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.opts.LoadTimeout)
		defer cancel()
		v, err := c.timedLoad(lctx, load)
		if err != nil {
			return nil, err
		}
		if store {
			c.store(lctx, key, v)
		}
		return v, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.([]byte), nil
	}
}

func (c *Cache) refreshInBackground(key string, load Loader) {
	// DoChan dedupes with any in-flight load for the same key; we do not
	// wait for the result.
	c.sf.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), c.opts.LoadTimeout)
		defer cancel()
		v, err := c.timedLoad(ctx, load)
		if err != nil {
			c.opts.Logger.Warn("background refresh failed", "key", key, "err", err)
			return nil, err
		}
		c.store(ctx, key, v)
		return v, nil
	})
}

func (c *Cache) store(ctx context.Context, key string, v []byte) {
	fresh := c.jitteredTTL()
	enc := encode(c.now().Add(fresh), v)
	if err := c.kv.Set(ctx, key, enc, fresh+c.opts.StaleFor); err != nil {
		c.opts.Logger.Warn("cache set failed", "key", key, "err", err)
	}
}

func (c *Cache) jitteredTTL() time.Duration {
	f := 1 + c.opts.Jitter*(2*c.rand()-1) // [1-j, 1+j)
	return time.Duration(float64(c.opts.TTL) * f)
}

func (c *Cache) timedLoad(ctx context.Context, load Loader) ([]byte, error) {
	start := time.Now()
	v, err := load(ctx)
	c.opts.Observer.Load(time.Since(start))
	return v, err
}

// ProductKeyPrefix namespaces product-detail entries in the KV.
const ProductKeyPrefix = "shoplab:product:"

// ProductKey is the cache key for a product's detail page.
func ProductKey(id int64) string { return ProductKeyPrefix + strconv.FormatInt(id, 10) }

// Fixed-mode entries are an 8-byte big-endian freshness deadline (unix nanos)
// followed by the payload.
const headerLen = 8

var errShortEntry = errors.New("cache: short entry")

func encode(freshUntil time.Time, payload []byte) []byte {
	out := make([]byte, headerLen+len(payload))
	binary.BigEndian.PutUint64(out, uint64(freshUntil.UnixNano()))
	copy(out[headerLen:], payload)
	return out
}

func decode(b []byte) (time.Time, []byte, error) {
	// Naive-mode entries are raw JSON, which always starts with '{'; a
	// valid header never does for any realistic timestamp.
	if len(b) < headerLen || b[0] == '{' {
		return time.Time{}, nil, errShortEntry
	}
	ns := int64(binary.BigEndian.Uint64(b[:headerLen]))
	return time.Unix(0, ns), b[headerLen:], nil
}
