package scenario

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ModeReplay replays a recorded arrival pattern (load.mode: replay).
const ModeReplay = "replay"

// ExecReplay is the executor of replay plans.
const ExecReplay = "replay"

// MaxReplaySpan is the longest a replay may last, after speed.
const MaxReplaySpan = 7 * 24 * time.Hour

// Replay reproduces recorded traffic: every request in an access log or
// HAR file is sent at its recorded time (divided by Speed), as an open
// model, so the target sees the recorded arrival pattern.
type Replay struct {
	// File is an access log (common/combined format) or a HAR recording.
	File string `yaml:"file" json:"file"`
	// Format is auto (default), log or har.
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
	// Speed divides recorded gaps: 2 replays an hour in 30 minutes.
	Speed float64 `yaml:"speed,omitempty" json:"speed,omitempty"`
	// Limit caps the number of requests replayed (default 100000).
	Limit int `yaml:"limit,omitempty" json:"limit,omitempty"`
	// Host keeps only HAR requests to this host (default: the most
	// frequent host in the recording).
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
	// Static keeps requests for static assets (.js, .css, images, fonts),
	// which are skipped by default.
	Static bool `yaml:"static,omitempty" json:"static,omitempty"`

	rec *Recording
}

// Recording is a loaded replay file.
type Recording struct {
	// Endpoints are the distinct method and path templates, in order of
	// first appearance. Each becomes a journey.
	Endpoints []Endpoint
	// Arrivals are the requests to send, ordered by At.
	Arrivals []Arrival
	// Skipped counts lines or entries left out (unparseable, static,
	// other hosts, over the limit).
	Skipped int
}

// Endpoint is one method and path template, e.g. GET /api/products/{id}.
type Endpoint struct {
	Method   string
	Template string
	HasBody  bool
	Count    int
}

// JourneyName names the endpoint's journey. Journey names cannot hold
// '/', so GET /api/products/{id} becomes "GET api.products.{id}".
func (ep Endpoint) JourneyName() string {
	p := strings.ReplaceAll(strings.Trim(ep.Template, "/"), "/", ".")
	if p == "" || strings.HasPrefix(p, " ") {
		p = "(root)" + p
	}
	return ep.Method + " " + p
}

// Arrival is one recorded request.
type Arrival struct {
	At       time.Duration // since the first request, after Speed
	Endpoint int
	// Target is the path and query to request.
	Target      string
	Body        string
	ContentType string
}

// Recording returns the loaded recording, or nil before LoadReplay.
func (r *Replay) Recording() *Recording {
	if r == nil {
		return nil
	}
	return r.rec
}

// Duration is when the last request is sent.
func (r *Recording) Duration() time.Duration {
	if len(r.Arrivals) == 0 {
		return 0
	}
	return r.Arrivals[len(r.Arrivals)-1].At
}

// RatePerSecond counts arrivals in each second.
func (r *Recording) RatePerSecond() []float64 {
	n := int(r.Duration()/time.Second) + 1
	out := make([]float64, n)
	for _, a := range r.Arrivals {
		out[int(a.At/time.Second)]++
	}
	return out
}

// LoadReplay reads the replay recording and turns each endpoint into a
// one-step journey. Call it after ResolvePaths; it does nothing unless
// load.mode is replay, and nothing the second time.
func (s *Scenario) LoadReplay() error {
	r := s.Load.Replay
	if s.Load.Mode != ModeReplay || r == nil || r.rec != nil {
		return nil
	}
	b, err := os.ReadFile(r.File) //nolint:gosec // the scenario names its recording; servers confine it to --data-dir
	if err != nil {
		return fmt.Errorf("load.replay.file: %w", err)
	}
	rec, err := ParseRecording(b, *r)
	if err != nil {
		return fmt.Errorf("load.replay.file %s: %w", r.File, err)
	}
	// Journeys already present must be the ones this recording produces
	// (a server ships the expanded scenario to its workers); hand-written
	// journeys are an error.
	if len(s.Journeys) > 0 {
		same := len(s.Journeys) == len(rec.Endpoints)
		for i := 0; same && i < len(rec.Endpoints); i++ {
			same = s.Journeys[i].Name == rec.Endpoints[i].JourneyName()
		}
		if !same {
			return errors.New("journeys: a replay scenario takes its requests from the recording; remove the journeys")
		}
	}
	var doc strings.Builder
	for _, ep := range rec.Endpoints {
		method := strings.ToLower(ep.Method)
		fmt.Fprintf(&doc, "- name: %q\n  weight: %d\n  steps:\n    - name: %q\n      %s: \"${replay.target}\"\n", ep.JourneyName(), max(ep.Count, 1), ep.Method+" "+ep.Template, method)
		if ep.HasBody {
			doc.WriteString("      body: \"${replay.body}\"\n      headers: { Content-Type: \"${replay.contentType}\" }\n")
		}
	}
	var js []Journey
	if err := yaml.Unmarshal([]byte(doc.String()), &js); err != nil {
		return fmt.Errorf("replay journeys: %w", err)
	}
	s.Journeys = js
	r.rec = rec
	return nil
}

var (
	logLineRe = regexp.MustCompile(`^\S+ \S+ \S+ \[([^\]]+)\] "([A-Z]+) (\S+)[^"]*"`)
	staticRe  = regexp.MustCompile(`(?i)\.(js|mjs|css|map|png|jpe?g|gif|svg|ico|webp|avif|woff2?|ttf|otf|eot|mp4|webm)$`)
	methods   = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}
)

type rawRequest struct {
	at          time.Time
	method      string
	target      string
	body        string
	contentType string
}

// ParseRecording reads an access log or HAR file into a recording.
func ParseRecording(b []byte, opt Replay) (*Recording, error) {
	format := opt.Format
	if format == "" || format == "auto" {
		format = "log"
		if t := bytes.TrimSpace(b); len(t) > 0 && t[0] == '{' {
			format = "har"
		}
	}
	var reqs []rawRequest
	skipped := 0
	var err error
	switch format {
	case "log":
		reqs, skipped = parseAccessLog(b)
	case "har":
		reqs, skipped, err = parseHAR(b, opt.Host)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("format must be auto, log or har, got %q", opt.Format)
	}
	if !opt.Static {
		kept := reqs[:0]
		for _, r := range reqs {
			p, _, _ := strings.Cut(r.target, "?")
			if staticRe.MatchString(p) {
				skipped++
				continue
			}
			kept = append(kept, r)
		}
		reqs = kept
	}
	if len(reqs) == 0 {
		return nil, errors.New("no replayable requests found (access log lines need a [timestamp] and \"METHOD /path\"; HAR entries need startedDateTime)")
	}
	sort.SliceStable(reqs, func(i, j int) bool { return reqs[i].at.Before(reqs[j].at) })
	limit := opt.Limit
	if limit <= 0 {
		limit = 100000
	}
	if len(reqs) > limit {
		skipped += len(reqs) - limit
		reqs = reqs[:limit]
	}
	speed := opt.Speed
	if speed <= 0 {
		speed = 1
	}
	// A plan holds a rate for every second of the replay, so its span is
	// bounded; the check is in float seconds so a huge span or a tiny
	// speed cannot overflow a Duration first.
	if span := reqs[len(reqs)-1].at.Sub(reqs[0].at).Seconds() / speed; !(span <= MaxReplaySpan.Seconds()) {
		return nil, fmt.Errorf("the recording spans %s at speed %g; a replay can last at most %s (raise load.replay.speed or trim the file)",
			reqs[len(reqs)-1].at.Sub(reqs[0].at).Round(time.Second), speed, MaxReplaySpan)
	}
	rec := &Recording{Skipped: skipped}
	index := map[string]int{}
	t0 := reqs[0].at
	for _, r := range reqs {
		p, _, _ := strings.Cut(r.target, "?")
		hasBody := r.body != ""
		key := r.method + " " + PathTemplate(p)
		if hasBody {
			key += " +body"
		}
		i, ok := index[key]
		if !ok {
			i = len(rec.Endpoints)
			index[key] = i
			rec.Endpoints = append(rec.Endpoints, Endpoint{Method: r.method, Template: PathTemplate(p), HasBody: hasBody})
		}
		rec.Endpoints[i].Count++
		at := time.Duration(float64(r.at.Sub(t0)) / speed)
		rec.Arrivals = append(rec.Arrivals, Arrival{At: at, Endpoint: i, Target: r.target, Body: r.body, ContentType: r.contentType})
	}
	// Journey names must be unique: an endpoint seen both with and without
	// a body keeps two journeys.
	plain := map[string]bool{}
	for _, ep := range rec.Endpoints {
		if !ep.HasBody {
			plain[ep.Method+" "+ep.Template] = true
		}
	}
	for i, ep := range rec.Endpoints {
		if ep.HasBody && plain[ep.Method+" "+ep.Template] {
			rec.Endpoints[i].Template += " (with body)"
		}
	}
	return rec, nil
}

func parseAccessLog(b []byte) ([]rawRequest, int) {
	var out []rawRequest
	skipped := 0
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := logLineRe.FindStringSubmatch(line)
		if m == nil || !methods[m[2]] {
			skipped++
			continue
		}
		at, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[1])
		if err != nil {
			skipped++
			continue
		}
		target := m[3]
		if u, err := url.Parse(target); err == nil && u.IsAbs() {
			target = u.RequestURI()
		}
		if !strings.HasPrefix(target, "/") {
			skipped++
			continue
		}
		out = append(out, rawRequest{at: at, method: m[2], target: target})
	}
	return out, skipped
}

func parseHAR(b []byte, host string) ([]rawRequest, int, error) {
	var har struct {
		Log struct {
			Entries []struct {
				StartedDateTime time.Time `json:"startedDateTime"`
				Request         struct {
					Method   string `json:"method"`
					URL      string `json:"url"`
					PostData *struct {
						MimeType string `json:"mimeType"`
						Text     string `json:"text"`
					} `json:"postData"`
				} `json:"request"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(b, &har); err != nil {
		return nil, 0, fmt.Errorf("not a HAR file: %w", err)
	}
	if host == "" {
		count := map[string]int{}
		for _, e := range har.Log.Entries {
			if u, err := url.Parse(e.Request.URL); err == nil {
				count[u.Host]++
			}
		}
		best := 0
		for h, n := range count {
			if n > best || n == best && h < host {
				host, best = h, n
			}
		}
	}
	var out []rawRequest
	skipped := 0
	for _, e := range har.Log.Entries {
		u, err := url.Parse(e.Request.URL)
		if err != nil || u.Host != host || !methods[e.Request.Method] || e.StartedDateTime.IsZero() {
			skipped++
			continue
		}
		r := rawRequest{at: e.StartedDateTime, method: e.Request.Method, target: u.RequestURI()}
		if pd := e.Request.PostData; pd != nil && pd.Text != "" {
			r.body, r.contentType = pd.Text, pd.MimeType
		}
		out = append(out, r)
	}
	return out, skipped, nil
}

var (
	uuidSegRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	numSegRe   = regexp.MustCompile(`^\d+$`)
	hexSegRe   = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	tokenSegRe = regexp.MustCompile(`^[A-Za-z0-9_-]{24,}$`)
)

// PathTemplate replaces ids in a path with placeholders:
// /api/orders/42/items/9f1c... becomes /api/orders/{id}/items/{hex}.
func PathTemplate(p string) string {
	if p == "" {
		return "/"
	}
	segs := strings.Split(path.Clean("/"+p), "/")
	for i, s := range segs {
		switch {
		case numSegRe.MatchString(s):
			segs[i] = "{id}"
		case uuidSegRe.MatchString(s):
			segs[i] = "{uuid}"
		case hexSegRe.MatchString(s):
			segs[i] = "{hex}"
		case tokenSegRe.MatchString(s) && strings.ContainsAny(s, "0123456789"):
			segs[i] = "{token}"
		}
	}
	return strings.Join(segs, "/")
}

// replayPlan builds the plan of a loaded recording: an open model whose
// planned rate per second is the recorded one.
func (l Load) replayPlan() (*Plan, error) {
	rec := l.Replay.Recording()
	if rec == nil {
		return nil, errors.New("the replay recording is not loaded")
	}
	rates := rec.RatePerSecond()
	peak := 0.0
	for _, r := range rates {
		peak = math.Max(peak, r)
	}
	p := &Plan{
		Mode: ModeRate, Executor: ExecReplay, Value: peak, Rates: rates,
		Duration: rec.Duration() + time.Second, GracefulStop: l.GracefulStop.D(), MaxVUs: l.MaxVUs,
	}
	if p.MaxVUs == 0 {
		p.MaxVUs = int(math.Min(50000, math.Max(50, math.Ceil(peak*5))))
	}
	return p, nil
}
