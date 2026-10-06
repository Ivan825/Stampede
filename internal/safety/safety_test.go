package safety

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

func TestPrivateAddrs(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "10.1.2.3": true, "192.168.0.10": true, "172.16.5.4": true,
		"::1": true, "fd00::1": true, "169.254.1.1": true,
		"8.8.8.8": false, "1.1.1.1": false, "2606:4700::1111": false,
	} {
		if got := IsPrivateAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: got %v", addr, got)
		}
	}
	ok, err := IsPrivateHost(context.Background(), "localhost")
	if !ok || err != nil {
		t.Errorf("localhost should be private")
	}
}

func TestCheckPlan(t *testing.T) {
	p := &scenario.Plan{Mode: scenario.ModeRate, Value: 500, Duration: time.Hour, MaxVUs: 2500}
	err := CheckPlan(p, UnverifiedPublicCaps)
	if err == nil || !strings.Contains(err.Error(), "rate 500/s") || !strings.Contains(err.Error(), "duration") {
		t.Errorf("got %v", err)
	}
	small := &scenario.Plan{Mode: scenario.ModeVUs, Value: 10, VUs: 10, Duration: time.Minute}
	if err := CheckPlan(small, UnverifiedPublicCaps); err != nil {
		t.Errorf("small plan rejected: %v", err)
	}
}

func TestHostPolicy(t *testing.T) {
	p := NewHostPolicy("shop.example.com", []string{"cdn.example.com"})
	for raw, want := range map[string]bool{
		"https://shop.example.com/x": true, "https://SHOP.example.com/x": true,
		"https://cdn.example.com/a": true, "http://127.0.0.1:9/": true,
		"https://203.0.113.9/": false,
	} {
		u, _ := url.Parse(raw)
		if got := p.Allow(u); got != want {
			t.Errorf("%s: got %v", raw, got)
		}
	}
}

func TestVerifyWellKnown(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok := Token(secret, "127.0.0.1")
	if tok != Token(secret, "127.0.0.1") || len(tok) != 32 {
		t.Fatal("token must be deterministic and 32 chars")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == WellKnownPath {
			w.Write([]byte(tok + "\n"))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	method, err := Verify(context.Background(), u, tok)
	if err != nil || method != "well-known" {
		t.Fatalf("verify: %v %s", err, method)
	}
	if _, err := Verify(context.Background(), u, "wrong"); err == nil {
		t.Error("wrong token verified")
	}
}
