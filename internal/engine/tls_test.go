package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// New connections (keep-alive off) resume TLS sessions per user, across
// users, or never, as target.http.tlsResumption says.
func TestTLSResumption(t *testing.T) {
	var full, resumed atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.DidResume {
			resumed.Add(1)
		} else {
			full.Add(1)
		}
	}))
	defer srv.Close()
	runOnce := func(mode string) (int64, int64) {
		full.Store(0)
		resumed.Store(0)
		run(t, fmt.Sprintf(`
metadata: {name: tls}
target: {baseURL: %q, http: {insecureSkipVerify: true, disableKeepAlive: true, tlsResumption: %s}}
journeys: [{name: a, steps: [{get: /}]}]
load: {iterations: 30, vus: 3}`, srv.URL, mode), nil)
		return full.Load(), resumed.Load()
	}
	// Per user: each run's three users start with no sessions.
	for range 2 {
		if f, r := runOnce("per-vu"); f != 3 || r != 27 {
			t.Errorf("per-vu: %d full handshakes, %d resumed", f, r)
		}
	}
	// Shared: sessions outlive the users, so a second run resumes them all.
	if f, r := runOnce("shared"); f > 3 || f+r != 30 {
		t.Errorf("shared, first run: %d full, %d resumed", f, r)
	}
	if f, r := runOnce("shared"); f != 0 || r != 30 {
		t.Errorf("shared, second run: %d full, %d resumed", f, r)
	}
	if f, r := runOnce("off"); f != 30 || r != 0 {
		t.Errorf("off: %d full, %d resumed", f, r)
	}
}
