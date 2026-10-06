package netem

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func client(p Profile) *http.Client {
	d := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{DialContext: Dialer(p, d.DialContext), DisableKeepAlives: false}}
}

func TestLatencyAdded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer srv.Close()
	c := client(Profile{RTT: 100 * time.Millisecond})
	// First request: connect (1 RTT) + exchange (1 RTT).
	start := time.Now()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if took := time.Since(start); took < 190*time.Millisecond || took > 400*time.Millisecond {
		t.Errorf("first request took %s, want about 200ms", took)
	}
	// Reused connection: one RTT.
	start = time.Now()
	resp, _ = c.Get(srv.URL)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if took := time.Since(start); took < 95*time.Millisecond || took > 250*time.Millisecond {
		t.Errorf("second request took %s, want about 100ms", took)
	}
}

func TestBandwidthLimited(t *testing.T) {
	body := strings.Repeat("x", 200_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
	defer srv.Close()
	c := client(Profile{Down: 400_000}) // 200 KB at 400 KB/s: about 0.5s
	start := time.Now()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	took := time.Since(start)
	if n != int64(len(body)) || took < 400*time.Millisecond || took > 1500*time.Millisecond {
		t.Errorf("200KB at 400KB/s took %s (%d bytes)", took, n)
	}
}

func TestJitterAndLoss(t *testing.T) {
	p := Profile{RTT: 100 * time.Millisecond, Jitter: 40 * time.Millisecond}
	lo, hi := time.Hour, time.Duration(0)
	for i := 0; i < 2000; i++ {
		d := p.halfRTT()
		lo, hi = min(lo, d), max(hi, d)
	}
	if lo < 30*time.Millisecond || hi > 70*time.Millisecond || hi-lo < 30*time.Millisecond {
		t.Errorf("half RTT with jitter ranged %s..%s, want about 30ms..70ms", lo, hi)
	}
	l := Profile{RTT: 50 * time.Millisecond, Loss: 0.1}
	stalls := 0
	for i := 0; i < 10000; i++ {
		if d := l.lossStall(); d > 0 {
			stalls++
			if d != 200*time.Millisecond {
				t.Fatalf("stall %s, want the 200ms minimum RTO", d)
			}
		}
	}
	if stalls < 850 || stalls > 1150 {
		t.Errorf("%d stalls in 10000 draws at 10%% loss", stalls)
	}
	if (Profile{RTT: time.Second}).rto() != 1500*time.Millisecond {
		t.Error("RTO should be 1.5 x RTT when that is larger")
	}
}

func TestParseBandwidth(t *testing.T) {
	for in, want := range map[string]int64{"1.6mbps": 200_000, "768kbps": 96_000, "8000": 1000, "1gbps": 125_000_000} {
		got, err := ParseBandwidth(in)
		if err != nil || got != want {
			t.Errorf("%s: %d %v", in, got, err)
		}
	}
	if _, err := ParseBandwidth("fast"); err == nil {
		t.Error("expected error")
	}
	if len(Names()) != 4 {
		t.Error(Names())
	}
}

func TestDialHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	d := &net.Dialer{}
	if _, err := Dialer(Profile{RTT: time.Second}, d.DialContext)(ctx, "tcp", "127.0.0.1:1"); err == nil {
		t.Error("expected the context to end the dial")
	}
}
