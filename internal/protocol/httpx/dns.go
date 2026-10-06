package httpx

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// DNSCache resolves host names once per TTL and shares the answer between
// virtual users, the way an operating system's resolver cache does for
// real clients. Without it every new connection waits on a lookup, which
// skews connect timing and can stall a load generator during ramp-up.
type DNSCache struct {
	TTL      time.Duration
	Resolver *net.Resolver
	mu       sync.Mutex
	entries  map[string]*dnsEntry
}

type dnsEntry struct {
	addrs   []netip.Addr
	expires time.Time
	next    atomic.Uint32
	// ready is closed once the lookup finishes, so concurrent callers for
	// the same host share one lookup.
	ready chan struct{}
	err   error
}

// NewDNSCache caches answers for ttl (default 30s).
func NewDNSCache(ttl time.Duration) *DNSCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &DNSCache{TTL: ttl, Resolver: net.DefaultResolver, entries: map[string]*dnsEntry{}}
}

// Lookup returns the next address for host, round-robin across answers.
func (c *DNSCache) Lookup(ctx context.Context, host string) (netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return a, nil
	}
	c.mu.Lock()
	e := c.entries[host]
	if e == nil || time.Now().After(e.expires) {
		e = &dnsEntry{ready: make(chan struct{}), expires: time.Now().Add(c.TTL)}
		c.entries[host] = e
		c.mu.Unlock()
		e.addrs, e.err = c.Resolver.LookupNetIP(ctx, "ip", host)
		if e.err != nil || len(e.addrs) == 0 {
			// Do not cache failures for long.
			e.expires = time.Now().Add(time.Second)
			if e.err == nil {
				e.err = &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
			}
		}
		close(e.ready)
	} else {
		c.mu.Unlock()
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return netip.Addr{}, ctx.Err()
	}
	if e.err != nil {
		return netip.Addr{}, e.err
	}
	i := e.next.Add(1) - 1
	return e.addrs[int(i)%len(e.addrs)], nil
}

// DialContext returns a dial function that resolves through the cache.
func (c *DNSCache) DialContext(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ip, err := c.Lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
}
