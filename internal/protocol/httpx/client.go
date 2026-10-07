// Package httpx is Stampede's HTTP/1.1 and HTTP/2 driver. It builds clients
// that behave like real users (per-user connection pools, cookie jars,
// TLS session reuse) and measures every request phase with httptrace.
package httpx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// Options configures a client.
type Options struct {
	HTTP2 bool
	// H2C speaks HTTP/2 without TLS to http:// URLs, with prior knowledge
	// (no upgrade from HTTP/1.1), as gRPC and many internal services do.
	H2C                bool
	DisableKeepAlive   bool
	InsecureSkipVerify bool
	// MaxRedirects: 0 means the default of 10, negative disables following.
	MaxRedirects int
	// MaxConnsPerHost bounds a shared pool (0 = unlimited).
	MaxConnsPerHost int
	// Dialer allows tests and network emulation to replace dialing.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// DNS caches lookups across connections; nil resolves on every dial.
	DNS *DNSCache
	// TLSResumption is where TLS sessions are kept for resumption: "shared"
	// (one cache for the process, the default), "per-transport" (a cache
	// per transport: per virtual user when each has its own) or "off"
	// (every connection makes a full handshake).
	TLSResumption string
}

var sessionCache = tls.NewLRUClientSessionCache(4096)

func sessionCacheFor(mode string) tls.ClientSessionCache {
	switch mode {
	case "off":
		return nil
	case "per-transport":
		return tls.NewLRUClientSessionCache(64)
	}
	return sessionCache
}

// NewTransport builds a transport. One per virtual user gives browser-like
// connection behaviour; one shared transport behaves like a service client.
func NewTransport(o Options) *http.Transport {
	t := &http.Transport{
		Proxy:                 nil,
		DialContext:           Dialer(o),
		ForceAttemptHTTP2:     o.HTTP2 || o.H2C,
		DisableKeepAlives:     o.DisableKeepAlive,
		DisableCompression:    false,
		MaxIdleConns:          0,
		MaxIdleConnsPerHost:   1024,
		MaxConnsPerHost:       o.MaxConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: o.InsecureSkipVerify, //nolint:gosec // opt-in for test targets
			ClientSessionCache: sessionCacheFor(o.TLSResumption),
			MinVersion:         tls.VersionTLS12,
		},
	}
	if o.H2C {
		// Without HTTP1 in the set, http:// URLs use HTTP/2 with prior
		// knowledge; https:// URLs still negotiate HTTP/2 over TLS.
		p := new(http.Protocols)
		p.SetUnencryptedHTTP2(true)
		p.SetHTTP2(true)
		t.Protocols = p
	}
	return t
}

// Dialer is the dial function the options describe: the test or
// emulation dialer if set, else a TCP dialer that uses the DNS cache.
// Other drivers (gRPC) dial through it so they resolve names the same way.
func Dialer(o Options) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if o.DialContext != nil {
		return o.DialContext
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if o.DNS != nil {
		return o.DNS.DialContext(d)
	}
	return d.DialContext
}

// NewClient wraps a transport with a fresh cookie jar and redirect policy.
func NewClient(t http.RoundTripper, o Options) *http.Client {
	jar, _ := cookiejar.New(nil)
	max := o.MaxRedirects
	if max == 0 {
		max = 10
	}
	return &http.Client{
		Transport: t,
		Jar:       jar,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if max < 0 || len(via) > max {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// ResetCookies gives the client an empty cookie jar, as for a new session.
func ResetCookies(c *http.Client) {
	jar, _ := cookiejar.New(nil)
	c.Jar = jar
}

// Result is a completed exchange.
type Result struct {
	Start    time.Time
	End      time.Time
	Phases   [metrics.NumPhases]time.Duration
	Status   int
	Header   http.Header
	Cookies  []*http.Cookie
	Body     []byte
	BytesIn  int64
	BytesOut int64
	Proto    string
	Reused   bool
	Err      error

	// trace receives httptrace callbacks; wrote and firstByte are its
	// timestamps, used by Finish.
	trace            *traceState
	wrote, firstByte time.Time
}

// Do sends req and reads the response. Up to maxBody bytes are kept in
// Result.Body; the rest is read and counted but discarded so connections
// can be reused and download time is measured fully. keepBody=false
// discards the whole body.
func Do(c *http.Client, req *http.Request, bodyLen int64, keepBody bool, maxBody int64) *Result {
	r, resp := Open(c, req, bodyLen)
	if resp == nil {
		return r
	}
	var (
		n   int64
		err error
	)
	if keepBody {
		lr := io.LimitReader(resp.Body, maxBody)
		r.Body, err = io.ReadAll(lr)
		n = int64(len(r.Body))
		if err == nil {
			var rest int64
			rest, err = io.Copy(io.Discard, resp.Body)
			n += rest
		}
	} else {
		n, err = io.Copy(io.Discard, resp.Body)
	}
	resp.Body.Close()
	r.Finish(n, err)
	return r
}

// Open sends req and returns once the response headers have arrived, for
// drivers that read a streamed body themselves. When the response is
// non-nil the caller must read and close its body, then call Finish. On a
// transport error the response is nil and the Result is complete.
func Open(c *http.Client, req *http.Request, bodyLen int64) (*Result, *http.Response) {
	r := &Result{}
	req = req.WithContext(Trace(req.Context(), r))

	r.Start = time.Now()
	resp, err := c.Do(req)
	r.collect()
	if err != nil {
		r.End = time.Now()
		r.Err = err
		return r, nil
	}
	r.Status = resp.StatusCode
	r.Header = resp.Header
	r.Cookies = resp.Cookies()
	r.Proto = resp.Proto
	r.BytesOut = estimateRequestSize(req, bodyLen)
	r.BytesIn = estimateHeaderSize(resp.Header) + int64(len(resp.Proto)+len(resp.Status)+4)
	return r, resp
}

// Trace returns a context that records the phases of the HTTP exchange
// made with it into r. Drivers whose library sends the request itself,
// such as a WebSocket handshake, use it to get the same phase timings,
// then call Finish.
func Trace(ctx context.Context, r *Result) context.Context {
	t := &traceState{}
	r.trace = t
	var dnsStart, connStart, tlsStart time.Time
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { t.mark(&dnsStart) },
		DNSDone:           func(httptrace.DNSDoneInfo) { t.since(metrics.PhaseDNS, &dnsStart) },
		ConnectStart:      func(string, string) { t.mark(&connStart) },
		ConnectDone:       func(string, string, error) { t.since(metrics.PhaseConnect, &connStart) },
		TLSHandshakeStart: func() { t.mark(&tlsStart) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { t.since(metrics.PhaseTLS, &tlsStart) },
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.reused = info.Reused
			t.mu.Unlock()
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.mark(&t.wrote) },
		GotFirstResponseByte: func() { t.mark(&t.firstByte) },
	})
}

// traceState collects httptrace callbacks. net/http may run dial
// callbacks on its own goroutine, and may finish a dial after the request
// was served by another connection, so the state is locked and stops
// accepting callbacks once the exchange has its response.
type traceState struct {
	mu               sync.Mutex
	closed           bool
	phases           [metrics.NumPhases]time.Duration
	wrote, firstByte time.Time
	reused           bool
}

func (t *traceState) mark(at *time.Time) {
	t.mu.Lock()
	if !t.closed {
		*at = time.Now()
	}
	t.mu.Unlock()
}

func (t *traceState) since(p metrics.Phase, start *time.Time) {
	t.mu.Lock()
	if !t.closed && !start.IsZero() {
		t.phases[p] += time.Since(*start)
	}
	t.mu.Unlock()
}

// collect copies the traced timings into r once; later callbacks belong
// to a connection this exchange did not use and are ignored.
func (r *Result) collect() {
	t := r.trace
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	for i, d := range t.phases {
		r.Phases[i] += d
	}
	r.wrote, r.firstByte, r.Reused = t.wrote, t.firstByte, t.reused
}

// Finish ends an exchange started by Open: bodyBytes were read from the
// response body and err is the error that stopped reading, if any.
func (r *Result) Finish(bodyBytes int64, err error) {
	r.End = time.Now()
	r.collect()
	r.BytesIn += bodyBytes
	if err != nil {
		r.Err = err
	}
	if !r.firstByte.IsZero() {
		if !r.wrote.IsZero() && r.firstByte.After(r.wrote) {
			r.Phases[metrics.PhaseWait] = r.firstByte.Sub(r.wrote)
		}
		r.Phases[metrics.PhaseDownload] = r.End.Sub(r.firstByte)
	}
}

func estimateHeaderSize(h http.Header) int64 {
	var n int64
	for k, vs := range h {
		for _, v := range vs {
			n += int64(len(k) + len(v) + 4)
		}
	}
	return n + 2
}

func estimateRequestSize(req *http.Request, bodyLen int64) int64 {
	n := int64(len(req.Method) + len(req.URL.RequestURI()) + len("HTTP/1.1") + 4)
	n += int64(len("Host: ") + len(req.Host) + 2)
	return n + estimateHeaderSize(req.Header) + bodyLen
}

// ClassifyError maps a transport error to a short, bounded label used in
// reports.
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	var tlsRec tls.RecordHeaderError
	var certErr *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &dnsErr):
		return "dns error"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "connection reset"
	case errors.Is(err, syscall.EADDRNOTAVAIL), errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return "local resource limit"
	case errors.As(err, &certErr), errors.As(err, &tlsRec):
		return "tls error"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:"):
		return "tls error"
	case strings.Contains(msg, "stopped after") && strings.Contains(msg, "redirects"):
		return "too many redirects"
	}
	return "network error"
}

// StatusError labels an HTTP error status.
func StatusError(code int) string { return fmt.Sprintf("HTTP %d", code) }
