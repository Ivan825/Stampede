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
	"syscall"
	"time"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// Options configures a client.
type Options struct {
	HTTP2              bool
	DisableKeepAlive   bool
	InsecureSkipVerify bool
	// MaxRedirects: 0 means the default of 10, negative disables following.
	MaxRedirects int
	// MaxConnsPerHost bounds a shared pool (0 = unlimited).
	MaxConnsPerHost int
	// Dialer allows tests and network emulation to replace dialing.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

var sessionCache = tls.NewLRUClientSessionCache(4096)

// NewTransport builds a transport. One per virtual user gives browser-like
// connection behaviour; one shared transport behaves like a service client.
func NewTransport(o Options) *http.Transport {
	dial := o.DialContext
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dial = d.DialContext
	}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     o.HTTP2,
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
			ClientSessionCache: sessionCache,
			MinVersion:         tls.VersionTLS12,
		},
	}
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
}

// Do sends req and reads the response. Up to maxBody bytes are kept in
// Result.Body; the rest is read and counted but discarded so connections
// can be reused and download time is measured fully. keepBody=false
// discards the whole body.
func Do(c *http.Client, req *http.Request, bodyLen int64, keepBody bool, maxBody int64) *Result {
	r := &Result{}
	var dnsStart, connStart, tlsStart, wrote, firstByte time.Time
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			if !dnsStart.IsZero() {
				r.Phases[metrics.PhaseDNS] += time.Since(dnsStart)
			}
		},
		ConnectStart: func(string, string) { connStart = time.Now() },
		ConnectDone: func(string, string, error) {
			if !connStart.IsZero() {
				r.Phases[metrics.PhaseConnect] += time.Since(connStart)
			}
		},
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			if !tlsStart.IsZero() {
				r.Phases[metrics.PhaseTLS] += time.Since(tlsStart)
			}
		},
		GotConn:              func(info httptrace.GotConnInfo) { r.Reused = info.Reused },
		WroteRequest:         func(httptrace.WroteRequestInfo) { wrote = time.Now() },
		GotFirstResponseByte: func() { firstByte = time.Now() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	r.Start = time.Now()
	resp, err := c.Do(req)
	if err != nil {
		r.End = time.Now()
		r.Err = err
		return r
	}
	r.Status = resp.StatusCode
	r.Header = resp.Header
	r.Cookies = resp.Cookies()
	r.Proto = resp.Proto
	r.BytesOut = estimateRequestSize(req, bodyLen)

	if keepBody {
		lr := io.LimitReader(resp.Body, maxBody)
		r.Body, err = io.ReadAll(lr)
		r.BytesIn = int64(len(r.Body))
		if err == nil {
			n, cerr := io.Copy(io.Discard, resp.Body)
			r.BytesIn += n
			err = cerr
		}
	} else {
		r.BytesIn, err = io.Copy(io.Discard, resp.Body)
	}
	resp.Body.Close()
	r.End = time.Now()
	r.BytesIn += estimateHeaderSize(resp.Header) + int64(len(resp.Proto)+len(resp.Status)+4)
	if err != nil {
		r.Err = err
	}

	if !firstByte.IsZero() {
		if !wrote.IsZero() && firstByte.After(wrote) {
			r.Phases[metrics.PhaseWait] = firstByte.Sub(wrote)
		}
		r.Phases[metrics.PhaseDownload] = r.End.Sub(firstByte)
	}
	return r
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
