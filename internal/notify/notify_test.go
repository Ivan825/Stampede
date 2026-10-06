package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sampleEvent() Event {
	return Event{
		ID: "d-1", Type: EventRunTargetFailed, At: time.Unix(1_700_000_000, 0).UTC(), Org: "acme",
		Message: "checkout missed 1 of 2 targets",
		Run: &Run{
			ID: "r-1", Project: "shop", Scenario: "checkout", Target: "https://shop.example.com", Status: "completed",
			Verdict: "fail", URL: "https://stampede.example.com/runs/r-1",
			Summary:       &Summary{Requests: 1200, ErrorRate: 0.0125, RPS: 40, P95: 0.8, P99: 1.25},
			FailedTargets: []string{"checkout.p95 < 500ms"},
		},
	}
}

// receiver is an httptest webhook receiver that fails the first n
// requests with status, then accepts.
type receiver struct {
	mu      sync.Mutex
	bodies  [][]byte
	headers []http.Header
	fail    int
	status  int
	calls   atomic.Int64
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	r.bodies = append(r.bodies, b)
	r.headers = append(r.headers, req.Header.Clone())
	r.mu.Unlock()
	if int(r.calls.Add(1)) <= r.fail {
		w.WriteHeader(r.status)
		_, _ = w.Write([]byte("try later"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func fastSender() *Sender {
	return &Sender{Backoff: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}
}

func TestWebhookSignedAndRetried(t *testing.T) {
	rcv := &receiver{fail: 2, status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	var attempts []Attempt
	ch := Channel{Kind: KindWebhook, URL: srv.URL + "/hook", Secret: "whsec", AllowPrivate: true}
	err := fastSender().Send(context.Background(), ch, sampleEvent(), func(a Attempt) { attempts = append(attempts, a) })
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 || attempts[0].OK || attempts[0].Status != 503 || !strings.Contains(attempts[0].Err, "try later") || !attempts[2].OK || attempts[2].N != 3 {
		t.Fatalf("attempts = %+v", attempts)
	}
	rcv.mu.Lock()
	defer rcv.mu.Unlock()
	body, h := rcv.bodies[2], rcv.headers[2]
	if !Verify([]byte("whsec"), body, h.Get(HeaderSignature)) || !strings.HasPrefix(h.Get(HeaderSignature), "sha256=") {
		t.Errorf("signature %q does not verify", h.Get(HeaderSignature))
	}
	if Verify([]byte("other"), body, h.Get(HeaderSignature)) {
		t.Error("signature verified with the wrong secret")
	}
	if h.Get(HeaderEvent) != EventRunTargetFailed || h.Get(HeaderDelivery) != "d-1" || h.Get(HeaderTimestamp) == "" {
		t.Errorf("headers %v", h)
	}
	if string(rcv.bodies[0]) != string(body) {
		t.Error("retries must send the same body")
	}
	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil || ev.Run.Verdict != "fail" || ev.Run.Summary.Requests != 1200 {
		t.Errorf("body %s: %v", body, err)
	}
}

func TestNoRetryOnClientError(t *testing.T) {
	rcv := &receiver{fail: 10, status: http.StatusNotFound}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	n := 0
	err := fastSender().Send(context.Background(), Channel{Kind: KindSlack, URL: srv.URL, AllowPrivate: true}, sampleEvent(), func(Attempt) { n++ })
	if err == nil || n != 1 || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err %v after %d attempts; a 404 should not be retried", err, n)
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	rcv := &receiver{fail: 10, status: http.StatusBadGateway}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	n := 0
	err := fastSender().Send(context.Background(), Channel{Kind: KindDiscord, URL: srv.URL, AllowPrivate: true}, sampleEvent(), func(Attempt) { n++ })
	if err == nil || n != 4 {
		t.Errorf("err %v after %d attempts, want 4 attempts", err, n)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
	}))
	defer srv.Close()
	start := time.Now()
	s := fastSender()
	if err := s.Send(context.Background(), Channel{Kind: KindSlack, URL: srv.URL, AllowPrivate: true}, sampleEvent(), nil); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 900*time.Millisecond {
		t.Errorf("retried after %v, want Retry-After's 1s", d)
	}
}

func TestPrivateDestinationsRefused(t *testing.T) {
	rcv := &receiver{}
	srv := httptest.NewServer(rcv)
	defer srv.Close()
	n := 0
	var last Attempt
	err := fastSender().Send(context.Background(), Channel{Kind: KindWebhook, URL: srv.URL}, sampleEvent(), func(a Attempt) { n++; last = a })
	if err == nil || n != 1 || !strings.Contains(last.Err, "refused") || rcv.calls.Load() != 0 {
		t.Fatalf("err %v, %d attempts, %d received: a loopback destination must be refused without retries", err, n, rcv.calls.Load())
	}
	if _, err := CheckURL(context.Background(), srv.URL, false); !errors.Is(err, ErrPrivateDestination) {
		t.Errorf("CheckURL(%s) = %v", srv.URL, err)
	}
	if _, err := CheckURL(context.Background(), srv.URL, true); err != nil {
		t.Errorf("allowed private destination refused: %v", err)
	}
	for _, u := range []string{"http://localhost:9/x", "http://169.254.169.254/latest/meta-data", "http://[::1]/", "http://10.1.2.3/", "http://metadata.google.internal/", "http://[::ffff:127.0.0.1]/"} {
		if _, err := CheckURL(context.Background(), u, false); !errors.Is(err, ErrPrivateDestination) {
			t.Errorf("CheckURL(%s) = %v, want refused", u, err)
		}
	}
	for _, u := range []string{"ftp://example.com/", "https://user:pw@example.com/", "https:///nohost"} {
		if _, err := CheckURL(context.Background(), u, true); err == nil {
			t.Errorf("CheckURL(%s) accepted", u)
		}
	}
	if _, err := CheckURL(context.Background(), "https://93.184.215.14/hook", false); err != nil {
		t.Errorf("public IP refused: %v", err)
	}
}

func TestRedirectsNotFollowed(t *testing.T) {
	inner := &receiver{}
	target := httptest.NewServer(inner)
	defer target.Close()
	srv := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer srv.Close()
	err := fastSender().Send(context.Background(), Channel{Kind: KindWebhook, URL: srv.URL, AllowPrivate: true}, sampleEvent(), nil)
	if err == nil || inner.calls.Load() != 0 {
		t.Errorf("redirect followed (err %v, target hit %d times)", err, inner.calls.Load())
	}
}

func TestIsInternal(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "10.0.0.1": true, "172.16.5.4": true, "192.168.1.1": true, "169.254.169.254": true,
		"100.64.0.1": true, "0.0.0.0": true, "::1": true, "fe80::1": true, "fc00::1": true, "::ffff:10.0.0.1": true,
		"64:ff9b::a00:1": true, "2002:a00:1::": true, "224.0.0.1": true,
		"8.8.8.8": false, "93.184.215.14": false, "2606:4700:4700::1111": false, "64:ff9b::808:808": false,
	} {
		if got := IsInternal(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsInternal(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestPayloads(t *testing.T) {
	ev := sampleEvent()
	ev.Run.Scenario = "<!channel> checkout"
	b, err := Payload(KindSlack, ev)
	if err != nil {
		t.Fatal(err)
	}
	var slack struct{ Text string }
	_ = json.Unmarshal(b, &slack)
	for _, want := range []string{"*Stampede · Targets failed*", "*FAIL*", "1200 requests at 40.0/s", "errors 1.25%", "p95 800ms", "p99 1.25s", "`checkout.p95 &lt; 500ms`", "<https://stampede.example.com/runs/r-1|Open the run>", "&lt;!channel&gt;"} {
		if !strings.Contains(slack.Text, want) {
			t.Errorf("Slack text lacks %q:\n%s", want, slack.Text)
		}
	}
	ev.Run.Scenario = "@everyone checkout"
	b, _ = Payload(KindDiscord, ev)
	var discord struct {
		Content         string
		AllowedMentions map[string][]string `json:"allowed_mentions"`
	}
	_ = json.Unmarshal(b, &discord)
	if !strings.Contains(discord.Content, "**FAIL**") || !strings.Contains(discord.Content, "[Open the run](<https://stampede.example.com/runs/r-1>)") ||
		strings.Contains(discord.Content, "@everyone") || discord.AllowedMentions["parse"] == nil {
		t.Errorf("Discord payload %s", b)
	}
	if _, err := Payload("email", ev); err == nil {
		t.Error("unknown kind accepted")
	}
}
