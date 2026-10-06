// Package notify delivers run events to notification channels: a generic
// webhook signed with HMAC-SHA256, a Slack incoming webhook or a Discord
// webhook. Deliveries are retried with backoff, every attempt is reported
// for the delivery log, and destinations on internal addresses are refused
// unless a channel explicitly allows them.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Ivan825/Stampede/internal/version"
)

// Channel kinds.
const (
	KindWebhook = "webhook"
	KindSlack   = "slack"
	KindDiscord = "discord"
)

// Kinds lists the channel kinds.
var Kinds = []string{KindWebhook, KindSlack, KindDiscord}

// Event types.
const (
	EventRunFinished     = "run.finished"
	EventRunTargetFailed = "run.target_failed"
	EventRunKilled       = "run.killed"
	// EventTest is sent by "send test" and ignores subscriptions.
	EventTest = "test"
)

// Events lists the event types a channel can subscribe to.
var Events = []string{EventRunFinished, EventRunTargetFailed, EventRunKilled}

// Signature headers of the generic webhook.
const (
	HeaderSignature = "X-Stampede-Signature"
	HeaderEvent     = "X-Stampede-Event"
	HeaderDelivery  = "X-Stampede-Delivery"
	HeaderTimestamp = "X-Stampede-Timestamp"
)

// Channel is a destination with its secrets decrypted.
type Channel struct {
	ID   string
	Name string
	Kind string
	URL  string
	// Secret signs generic webhook bodies.
	Secret       string
	AllowPrivate bool
}

// Event is what happened, as sent to a generic webhook.
type Event struct {
	// ID identifies the delivery; retries reuse it so receivers can
	// ignore duplicates.
	ID   string    `json:"id"`
	Type string    `json:"type"`
	At   time.Time `json:"at"`
	Org  string    `json:"organisation,omitempty"`
	Run  *Run      `json:"run,omitempty"`
	// Message is a one-line human summary.
	Message string `json:"message"`
}

// Run describes the run an event is about.
type Run struct {
	ID         string `json:"id"`
	Project    string `json:"project,omitempty"`
	Scenario   string `json:"scenario"`
	Target     string `json:"target,omitempty"`
	Status     string `json:"status"`
	Verdict    string `json:"verdict,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	// URL opens the run in the web UI (when the server knows its public URL).
	URL           string   `json:"url,omitempty"`
	Summary       *Summary `json:"summary,omitempty"`
	FailedTargets []string `json:"failedTargets,omitempty"`
	// KilledBy is set for run.killed.
	KilledBy string `json:"killedBy,omitempty"`
}

// Summary holds the headline numbers of a finished run.
type Summary struct {
	Requests  uint64  `json:"requests"`
	ErrorRate float64 `json:"errorRate"`
	RPS       float64 `json:"rps"`
	P95       float64 `json:"p95"`
	P99       float64 `json:"p99"`
}

// Attempt is the outcome of one delivery attempt.
type Attempt struct {
	N        int
	OK       bool
	Status   int
	Err      string
	Duration time.Duration
	At       time.Time
}

// Sender delivers events.
type Sender struct {
	// Client returns the HTTP client for a channel; the default is Client
	// with a 15 second timeout.
	Client func(allowPrivate bool) *http.Client
	// Backoff lists the waits before each retry; its length is the number
	// of retries. The default is 2s, 10s, 30s.
	Backoff []time.Duration
	// MaxRetryAfter caps a Retry-After header (default 60s).
	MaxRetryAfter time.Duration
}

// DefaultBackoff is the default retry schedule.
var DefaultBackoff = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

// Send delivers ev to ch, retrying transport errors, 408, 425, 429 and 5xx
// responses with backoff. record is called after every attempt. The error
// is that of the last attempt.
func (s *Sender) Send(ctx context.Context, ch Channel, ev Event, record func(Attempt)) error {
	body, err := Payload(ch.Kind, ev)
	if err != nil {
		return err
	}
	client := Client
	if s.Client != nil {
		client = func(p bool, _ time.Duration) *http.Client { return s.Client(p) }
	}
	hc := client(ch.AllowPrivate, 15*time.Second)
	backoff := s.Backoff
	if backoff == nil {
		backoff = DefaultBackoff
	}
	maxRA := s.MaxRetryAfter
	if maxRA <= 0 {
		maxRA = time.Minute
	}
	var last error
	for n := 1; ; n++ {
		a, retryAfter := s.attempt(ctx, hc, ch, ev, body)
		a.N = n
		if record != nil {
			record(a)
		}
		if a.OK {
			return nil
		}
		last = errors.New(a.Err)
		if !retryable(a) || n > len(backoff) {
			return last
		}
		wait := backoff[n-1]
		if retryAfter > wait {
			wait = min(retryAfter, maxRA)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return fmt.Errorf("%w (gave up: %w)", last, ctx.Err())
		}
	}
}

func retryable(a Attempt) bool {
	switch a.Status {
	case 0:
		return !strings.Contains(a.Err, ErrPrivateDestination.Error())
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return a.Status >= 500
	}
}

func (s *Sender) attempt(ctx context.Context, hc *http.Client, ch Channel, ev Event, body []byte) (Attempt, time.Duration) {
	a := Attempt{At: time.Now()}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.URL, bytes.NewReader(body))
	if err != nil {
		a.Err = err.Error()
		return a, 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Stampede-Notifications/"+version.Version)
	if ch.Kind == KindWebhook {
		ts := strconv.FormatInt(a.At.Unix(), 10)
		req.Header.Set(HeaderEvent, ev.Type)
		req.Header.Set(HeaderDelivery, ev.ID)
		req.Header.Set(HeaderTimestamp, ts)
		if ch.Secret != "" {
			req.Header.Set(HeaderSignature, Sign([]byte(ch.Secret), body))
		}
	}
	resp, err := hc.Do(req)
	a.Duration = time.Since(a.At)
	if err != nil {
		a.Err = cleanError(err)
		return a, 0
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	a.Status = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		a.OK = true
		return a, 0
	}
	a.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
	if t := strings.TrimSpace(string(snippet)); t != "" {
		if len(t) > 200 {
			t = t[:200] + "…"
		}
		a.Err += ": " + t
	}
	var retryAfter time.Duration
	if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 {
		retryAfter = time.Duration(v) * time.Second
	}
	return a, retryAfter
}

// cleanError keeps a transport error readable and free of the URL, which
// for Slack and Discord is itself the credential.
func cleanError(err error) string {
	if errors.Is(err, ErrPrivateDestination) {
		return "refused: " + ErrPrivateDestination.Error() + " (allow private destinations for this channel to deliver there)"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// Sign returns the X-Stampede-Signature value for body: "sha256=" and the
// hex HMAC-SHA256 of the body with the channel's secret.
func Sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a signature made by Sign in constant time.
func Verify(secret, body []byte, signature string) bool {
	return hmac.Equal([]byte(Sign(secret, body)), []byte(signature))
}

// Payload renders the request body for a channel kind.
func Payload(kind string, ev Event) ([]byte, error) {
	switch kind {
	case KindWebhook:
		return json.Marshal(ev)
	case KindSlack:
		return json.Marshal(map[string]any{"text": slackText(ev)})
	case KindDiscord:
		return json.Marshal(map[string]any{
			"username":         "Stampede",
			"content":          discordText(ev),
			"allowed_mentions": map[string]any{"parse": []string{}},
		})
	default:
		return nil, fmt.Errorf("unknown channel kind %q", kind)
	}
}

// headline is the event in a few words.
func headline(ev Event) string {
	switch ev.Type {
	case EventRunFinished:
		return "Run finished"
	case EventRunTargetFailed:
		return "Targets failed"
	case EventRunKilled:
		return "Run killed"
	case EventTest:
		return "Test notification"
	}
	return ev.Type
}

func verdictLabel(v string) string {
	switch v {
	case "pass":
		return "PASS"
	case "fail":
		return "FAIL"
	case "generator-limited":
		return "GENERATOR-LIMITED"
	case "no-targets":
		return "NO TARGETS"
	}
	return strings.ToUpper(v)
}

// lines renders the event as plain lines; link formats a link.
func lines(ev Event, bold func(string) string, code func(string) string, link func(text, url string) string) []string {
	out := []string{bold("Stampede · " + headline(ev))}
	if ev.Message != "" {
		out = append(out, ev.Message)
	}
	r := ev.Run
	if r == nil {
		return out
	}
	head := code(r.Scenario)
	if r.Target != "" {
		head += " against " + r.Target
	}
	if r.Verdict != "" {
		head += " · " + bold(verdictLabel(r.Verdict))
	}
	if r.Status != "" {
		head += " · " + r.Status
	}
	out = append(out, head)
	if s := r.Summary; s != nil {
		out = append(out, fmt.Sprintf("%d requests at %.1f/s · errors %.2f%% · p95 %s · p99 %s",
			s.Requests, s.RPS, s.ErrorRate*100, ms(s.P95), ms(s.P99)))
	}
	if len(r.FailedTargets) > 0 {
		ft := make([]string, len(r.FailedTargets))
		for i, t := range r.FailedTargets {
			ft[i] = code(t)
		}
		out = append(out, "Failed: "+strings.Join(ft, ", "))
	}
	if r.KilledBy != "" {
		out = append(out, "Killed by "+r.KilledBy)
	}
	if r.URL != "" {
		out = append(out, link("Open the run", r.URL))
	}
	return out
}

func ms(s float64) string {
	if s >= 1 {
		return strconv.FormatFloat(s, 'f', 2, 64) + "s"
	}
	return strconv.FormatFloat(s*1000, 'f', 0, 64) + "ms"
}

// slackEscape escapes the three characters Slack treats as control
// sequences in mrkdwn text.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func slackText(ev Event) string {
	ls := lines(ev,
		func(s string) string { return "*" + slackEscape(s) + "*" },
		func(s string) string { return "`" + slackEscape(s) + "`" },
		func(text, u string) string { return "<" + slackEscape(u) + "|" + slackEscape(text) + ">" },
	)
	return strings.Join(ls, "\n")
}

func discordText(ev Event) string {
	esc := strings.NewReplacer("`", "'", "@", "@\u200b")
	ls := lines(ev,
		func(s string) string { return "**" + esc.Replace(s) + "**" },
		func(s string) string { return "`" + esc.Replace(s) + "`" },
		func(text, u string) string { return "[" + text + "](<" + u + ">)" },
	)
	s := strings.Join(ls, "\n")
	if len(s) > 1900 {
		s = s[:1900] + "…"
	}
	return s
}
