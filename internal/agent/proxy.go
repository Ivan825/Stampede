// Package agent is stampede agent: a helper that runs inside the user's
// environment and injects faults into dependencies during a load test.
//
// It places TCP proxies in front of dependencies (a database, a cache, a
// downstream API) and, on request, adds latency and jitter, limits
// bandwidth, resets connections, refuses new ones or stops forwarding.
// With explicit flags it can also pause, stop or restart Docker containers
// and scale Kubernetes deployments. Every fault has a duration and is
// reverted when it ends, when it is cleared (the kill switch), or when the
// agent stops. Every action is written to an audit log.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// Fault is what a proxy does to traffic while the fault is active. The
// zero Fault forwards traffic untouched.
type Fault struct {
	// Latency is added to each direction of every connection, so a
	// request and its response together take twice Latency longer.
	Latency time.Duration `json:"latency,omitempty"`
	// Jitter adds a random delay in [0, Jitter) on top of Latency.
	Jitter time.Duration `json:"jitter,omitempty"`
	// Bandwidth caps each direction of each connection, in bytes per
	// second (0 = unlimited).
	Bandwidth int64 `json:"bandwidth,omitempty"`
	// Reset resets every open connection when the fault starts, and every
	// new connection while it lasts.
	Reset bool `json:"reset,omitempty"`
	// Refuse resets new connections as soon as they arrive; open ones are
	// left alone.
	Refuse bool `json:"refuse,omitempty"`
	// Blackhole stops forwarding in both directions: connections stay
	// open and requests time out.
	Blackhole bool `json:"blackhole,omitempty"`
}

// IsZero reports whether f changes nothing.
func (f Fault) IsZero() bool { return f == Fault{} }

// Proxy forwards TCP connections from Listen to Upstream, applying the
// current fault.
type Proxy struct {
	Name     string
	Listen   string
	Upstream string
	log      *slog.Logger

	ln net.Listener

	mu    sync.Mutex
	fault Fault
	conns map[*link]struct{}
	// changed is closed and replaced whenever the fault changes, waking
	// pumps waiting out a blackhole.
	changed chan struct{}
}

// NewProxy returns a proxy that is not yet listening.
func NewProxy(name, listen, upstream string, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{Name: name, Listen: listen, Upstream: upstream, log: log, conns: map[*link]struct{}{}, changed: make(chan struct{})}
}

// Start listens and serves until ctx ends or Close is called.
func (p *Proxy) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.Listen)
	if err != nil {
		return err
	}
	p.ln = ln
	go func() {
		<-ctx.Done()
		_ = p.Close()
	}()
	go p.serve(ctx)
	return nil
}

// Addr is the listening address.
func (p *Proxy) Addr() string {
	if p.ln == nil {
		return p.Listen
	}
	return p.ln.Addr().String()
}

// Close stops listening and closes every connection.
func (p *Proxy) Close() error {
	var err error
	if p.ln != nil {
		err = p.ln.Close()
	}
	p.mu.Lock()
	for l := range p.conns {
		l.close(false)
	}
	p.mu.Unlock()
	return err
}

// Fault returns the current fault.
func (p *Proxy) Fault() Fault {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fault
}

// Conns is the number of open connections.
func (p *Proxy) Conns() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// SetFault replaces the current fault. A fault with Reset resets every
// open connection.
func (p *Proxy) SetFault(f Fault) {
	p.mu.Lock()
	p.fault = f
	close(p.changed)
	p.changed = make(chan struct{})
	var reset []*link
	if f.Reset {
		for l := range p.conns {
			reset = append(reset, l)
		}
	}
	p.mu.Unlock()
	for _, l := range reset {
		l.close(true)
	}
}

func (p *Proxy) current() (Fault, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fault, p.changed
}

func (p *Proxy) serve(ctx context.Context) {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			p.log.Warn("accept failed", "proxy", p.Name, "error", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go p.handle(ctx, c)
	}
}

func (p *Proxy) handle(ctx context.Context, client net.Conn) {
	if f, _ := p.current(); f.Refuse || f.Reset {
		resetConn(client)
		return
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	up, err := d.DialContext(ctx, "tcp", p.Upstream)
	if err != nil {
		p.log.Warn("upstream unreachable", "proxy", p.Name, "upstream", p.Upstream, "error", err)
		resetConn(client)
		return
	}
	l := &link{client: client, up: up, closed: make(chan struct{})}
	p.mu.Lock()
	p.conns[l] = struct{}{}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.conns, l)
		p.mu.Unlock()
		l.close(false)
	}()
	done := make(chan struct{}, 2)
	go func() { p.pump(up, client, l.closed); done <- struct{}{} }()
	go func() { p.pump(client, up, l.closed); done <- struct{}{} }()
	// Each direction half-closes when its source ends; the link is done
	// when both have.
	<-done
	<-done
}

// link is one proxied connection.
type link struct {
	client, up net.Conn
	once       sync.Once
	closed     chan struct{}
}

func (l *link) close(reset bool) {
	l.once.Do(func() {
		close(l.closed)
		if reset {
			resetConn(l.client)
			resetConn(l.up)
			return
		}
		_ = l.client.Close()
		_ = l.up.Close()
	})
}

// resetConn closes c with a TCP RST instead of a FIN.
func resetConn(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = c.Close()
}

type chunk struct {
	b   []byte
	due time.Time
}

// pump copies src to dst. A reader stamps each chunk with when it may be
// delivered (arrival + latency + jitter, never earlier than the previous
// chunk, so bytes are not reordered); a writer delivers chunks on time and
// within the bandwidth limit. Delays apply in flight, so added latency does
// not lower throughput, as on a long link.
func (p *Proxy) pump(dst, src net.Conn, closed <-chan struct{}) {
	q := make(chan chunk, 256)
	go func() {
		defer close(q)
		var last time.Time
		for {
			buf := make([]byte, 32<<10)
			n, err := src.Read(buf)
			if n > 0 {
				f, _ := p.current()
				due := time.Now().Add(f.Latency)
				if f.Jitter > 0 {
					due = due.Add(rand.N(f.Jitter))
				}
				if due.Before(last) {
					due = last
				}
				last = due
				q <- chunk{b: buf[:n], due: due}
			}
			if err != nil {
				return
			}
		}
	}()
	// Whatever ends the writer, keep draining so the reader never blocks.
	defer func() {
		go func() {
			for range q {
			}
		}()
	}()
	var tokens float64
	lastFill := time.Now()
	for c := range q {
		// Wait out a blackhole: hold the data until the fault changes.
		for {
			f, changed := p.current()
			if !f.Blackhole {
				break
			}
			select {
			case <-changed:
			case <-closed:
				return
			}
		}
		if d := time.Until(c.due); d > 0 {
			select {
			case <-time.After(d):
			case <-closed:
				return
			}
		}
		b := c.b
		for len(b) > 0 {
			f, _ := p.current()
			n := len(b)
			if f.Bandwidth > 0 {
				now := time.Now()
				tokens = min(tokens+now.Sub(lastFill).Seconds()*float64(f.Bandwidth), float64(f.Bandwidth)/10)
				lastFill = now
				if tokens < 1 {
					time.Sleep(time.Duration((1 - tokens) / float64(f.Bandwidth) * float64(time.Second)))
					continue
				}
				n = min(n, int(tokens), 16<<10)
				tokens -= float64(n)
			}
			if _, err := dst.Write(b[:n]); err != nil {
				return
			}
			b = b[n:]
		}
	}
	// Half-close so the other side sees EOF while replies still flow.
	if tc, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = tc.CloseWrite()
	} else {
		_ = dst.Close()
	}
}
