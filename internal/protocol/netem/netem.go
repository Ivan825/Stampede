// Package netem emulates slower networks inside the load generator: added
// round-trip latency and per-connection bandwidth limits, applied to each
// virtual user's connections. It needs no privileges, so it works on any
// worker. (Packet loss and jitter at the kernel level need Linux netem and
// are not provided here.)
package netem

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Profile describes a network.
type Profile struct {
	Name string
	// RTT is added to every round trip: half before data is sent after
	// receiving, half before data is delivered after sending.
	RTT time.Duration
	// Down and Up are bandwidth limits in bytes per second (0 = unlimited).
	Down, Up int64
}

// Profiles are the built-in networks. Bandwidths are in bits per second
// in the names users know, stored here as bytes per second.
var Profiles = map[string]Profile{
	"slow-3g":   {Name: "slow-3g", RTT: 400 * time.Millisecond, Down: 400_000 / 8, Up: 400_000 / 8},
	"3g":        {Name: "3g", RTT: 300 * time.Millisecond, Down: 1_600_000 / 8, Up: 768_000 / 8},
	"4g":        {Name: "4g", RTT: 70 * time.Millisecond, Down: 12_000_000 / 8, Up: 6_000_000 / 8},
	"slow-wifi": {Name: "slow-wifi", RTT: 30 * time.Millisecond, Down: 2_000_000 / 8, Up: 1_000_000 / 8},
}

// Names lists the built-in profiles.
func Names() []string {
	out := make([]string, 0, len(Profiles))
	for n := range Profiles {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ParseBandwidth reads "1.6mbps", "768kbps", "100000" (bits per second)
// and returns bytes per second.
func ParseBandwidth(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1.0
	for _, u := range []struct {
		suffix string
		m      float64
	}{{"gbps", 1e9}, {"mbps", 1e6}, {"kbps", 1e3}, {"bps", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.m
			break
		}
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid bandwidth %q (use forms like 1.6mbps or 768kbps)", s)
	}
	return int64(f * mult / 8), nil
}

// Dialer wraps a dial function so every connection is shaped by p.
func Dialer(p Profile, dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// The TCP handshake is one round trip.
		if err := sleep(ctx, p.RTT); err != nil {
			return nil, err
		}
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &conn{Conn: c, p: p, down: newBucket(p.Down), up: newBucket(p.Up)}, nil
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// conn adds half the RTT whenever the direction of traffic changes, which
// adds one full RTT to each request/response exchange, and throttles bytes
// in each direction.
type conn struct {
	net.Conn
	p        Profile
	down, up *bucket
	mu       sync.Mutex
	sending  bool
}

func (c *conn) turn(toSending bool) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sending == toSending {
		return 0
	}
	c.sending = toSending
	return c.p.RTT / 2
}

func (c *conn) Write(b []byte) (int, error) {
	time.Sleep(c.turn(true))
	c.up.wait(len(b))
	return c.Conn.Write(b)
}

func (c *conn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		time.Sleep(c.turn(false))
		c.down.wait(n)
	}
	return n, err
}

// bucket is a token bucket for bytes.
type bucket struct {
	rate   int64 // bytes per second; 0 = unlimited
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newBucket(rate int64) *bucket {
	// Allow a burst of 16 KB (a few packets) so short exchanges are not
	// charged a full second's worth of shaping.
	return &bucket{rate: rate, tokens: 16 << 10, last: time.Now()}
}

func (b *bucket) wait(n int) {
	if b == nil || b.rate <= 0 {
		return
	}
	b.mu.Lock()
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * float64(b.rate)
	if burst := float64(16 << 10); b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	b.tokens -= float64(n)
	var d time.Duration
	if b.tokens < 0 {
		d = time.Duration(-b.tokens / float64(b.rate) * float64(time.Second))
	}
	b.mu.Unlock()
	time.Sleep(d)
}
