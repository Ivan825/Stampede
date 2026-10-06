package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ivan825/Stampede/internal/metrics"
)

// protoServer answers with the protocol the server saw, so a test can
// check both ends agree.
func protoServer(t *testing.T, tls, h2c bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	}))
	if h2c {
		p := new(http.Protocols)
		p.SetHTTP1(true)
		p.SetUnencryptedHTTP2(true)
		srv.Config.Protocols = p
	}
	if tls {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestProtocolNegotiation(t *testing.T) {
	tests := []struct {
		name     string
		tls, h2c bool // server
		opts     Options
		want     string
	}{
		{"TLS without http2 stays on HTTP/1.1", true, false, Options{InsecureSkipVerify: true}, "HTTP/1.1"},
		{"TLS with http2 negotiates h2", true, false, Options{InsecureSkipVerify: true, HTTP2: true}, "HTTP/2.0"},
		{"cleartext defaults to HTTP/1.1", false, true, Options{}, "HTTP/1.1"},
		{"cleartext with h2c uses prior knowledge", false, true, Options{H2C: true}, "HTTP/2.0"},
		{"h2c still negotiates h2 over TLS", true, false, Options{InsecureSkipVerify: true, H2C: true}, "HTTP/2.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := protoServer(t, tc.tls, tc.h2c)
			c := NewClient(NewTransport(tc.opts), tc.opts)
			defer c.CloseIdleConnections()
			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			r := Do(c, req, 0, true, 1<<20)
			if r.Err != nil {
				t.Fatal(r.Err)
			}
			if r.Proto != tc.want || string(r.Body) != tc.want {
				t.Errorf("client saw %s, server saw %s, want %s", r.Proto, r.Body, tc.want)
			}
		})
	}
}

func TestOpenStreamsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 1000))
	}))
	defer srv.Close()
	c := NewClient(NewTransport(Options{}), Options{})
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	r, resp := Open(c, req, 0)
	if resp == nil {
		t.Fatal(r.Err)
	}
	if !r.End.IsZero() {
		t.Error("Open must leave the exchange unfinished")
	}
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	r.Finish(n, err)
	if r.Err != nil || n != 1000 || r.BytesIn <= 1000 {
		t.Errorf("err=%v body=%d bytesIn=%d", r.Err, n, r.BytesIn)
	}
	if r.End.Before(r.Start) || r.Phases[metrics.PhaseDownload] <= 0 && r.Phases[metrics.PhaseWait] <= 0 {
		t.Errorf("timings not recorded: %+v", r)
	}
}
