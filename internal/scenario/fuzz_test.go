package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fuzz tests feed the parsers untrusted input: scenario files come
// from users and the web UI, replay recordings from anywhere. Each must
// return an error rather than panic or hang. Run one with, for example,
//
//	go test ./internal/scenario -run '^$' -fuzz FuzzParse -fuzztime 30s

// FuzzParse parses scenario documents and compiles the ones that are
// valid.
func FuzzParse(f *testing.F) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte(`{"metadata":{"name":"j"},"target":{"baseURL":"http://x"},"journeys":[{"name":"a","steps":[{"get":"/"}]}],"load":{"iterations":1}}`))
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > 64<<10 {
			return
		}
		s, err := Parse(src)
		if err != nil {
			return
		}
		if _, err := Compile(s); err != nil {
			return
		}
		if _, err := s.Load.Plan(); err != nil {
			return
		}
	})
}

// FuzzTemplate compiles ${} templates and renders the ones that compile.
func FuzzTemplate(f *testing.F) {
	for _, s := range []string{
		"plain", "/p/${id}", "${n * 2}x", "https://${env.HOST}/a", "${data.users.email}",
		"$${literal}", `${ {"k": id}.k }`, "${n > 2 ? 'many' : 'few'}", "${toJSON([1, 2])}",
		"${rand(1, 10)}", "${uuid()}", "${", "${}", "}${", "$${${id}}", `${"}"}`,
	} {
		f.Add(s)
	}
	sc, err := NewScope("id", "n")
	if err != nil {
		f.Fatal(err)
	}
	vars := act(map[string]any{
		"id":   "p-42",
		"n":    int64(3),
		"env":  map[string]string{"HOST": "shop.test"},
		"data": map[string]any{"users": map[string]any{"email": "a@b.c"}},
	})
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 512 {
			return
		}
		tp, err := sc.CompileTemplate(src)
		if err != nil {
			return
		}
		out, err := tp.Render(vars)
		if err == nil && tp.IsLiteral() && !strings.Contains(src, "$${") && out != src {
			t.Errorf("literal %q rendered as %q", src, out)
		}
	})
}

// FuzzParseRecording parses access logs and HAR files for replay.
func FuzzParseRecording(f *testing.F) {
	f.Add([]byte(accessLog), "")
	f.Add([]byte(har), "")
	f.Add([]byte(har), "shop.example")
	f.Add([]byte(`10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] "GET http://h/a?b=1 HTTP/1.1" 200 1`), "")
	f.Add([]byte("10.0.0.1 - - [07/Oct/1026:10:00:00 +0000] \"GET /a HTTP/1.1\" 200 1\n10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] \"GET /b HTTP/1.1\" 200 1\n"), "")
	f.Add([]byte(`{"log":{"entries":[{"startedDateTime":"2026-10-07T10:00:00Z","request":{"method":"POST","url":"http://h/x","postData":{"mimeType":"application/json","text":"{}"}}}]}}`), "")
	f.Fuzz(func(t *testing.T, b []byte, host string) {
		if len(b) > 64<<10 {
			return
		}
		rec, err := ParseRecording(b, Replay{Host: host, Speed: 2, Limit: 1000})
		if err != nil {
			return
		}
		for i, a := range rec.Arrivals {
			if a.Endpoint < 0 || a.Endpoint >= len(rec.Endpoints) {
				t.Fatalf("arrival %d points at endpoint %d of %d", i, a.Endpoint, len(rec.Endpoints))
			}
			if a.At < 0 || (i > 0 && a.At < rec.Arrivals[i-1].At) {
				t.Fatalf("arrival %d at %v is out of order", i, a.At)
			}
		}
		if rec.Duration() > MaxReplaySpan {
			t.Fatalf("a replay of %v was accepted", rec.Duration())
		}
		total := 0.0
		for _, n := range rec.RatePerSecond() {
			total += n
		}
		if int(total) != len(rec.Arrivals) {
			t.Fatalf("rates add up to %v, want %d", total, len(rec.Arrivals))
		}
		seen := map[string]bool{}
		for _, ep := range rec.Endpoints {
			if seen[ep.JourneyName()] {
				t.Fatalf("two endpoints share the journey name %q", ep.JourneyName())
			}
			seen[ep.JourneyName()] = true
		}
	})
}

// FuzzParseThreshold parses target expressions such as "http.p95 < 500ms".
func FuzzParseThreshold(f *testing.F) {
	for _, s := range []string{
		"http.p95 < 500ms", "errors < 1%", "checkout.p99.9 <= 2s", "browse/GET /api/x.max < 1s",
		"rps >= 100/s", "checks > 99%", "p95 500ms", "http.p42 < 1s", "errors < 200%", "p95 < fast",
		"book/hold seats.p95 < 300ms", "ttfb.p95 < 2s", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		th, err := ParseThreshold(src)
		if err != nil {
			return
		}
		_ = th.Pass(th.Value)
		if th.Op == "" || th.Metric == "" {
			t.Fatalf("%q parsed without an operator or metric: %+v", src, th)
		}
	})
}
