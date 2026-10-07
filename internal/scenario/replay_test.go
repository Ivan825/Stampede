package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const accessLog = `10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] "GET /api/products?page=1 HTTP/1.1" 200 512 "-" "Mozilla/5.0"
10.0.0.2 - - [07/Oct/2026:10:00:00 +0000] "GET /static/app.js HTTP/1.1" 200 9000 "-" "Mozilla/5.0"
10.0.0.1 - - [07/Oct/2026:10:00:01 +0000] "GET /api/products/42 HTTP/1.1" 200 128 "-" "Mozilla/5.0"
garbage line
10.0.0.3 - - [07/Oct/2026:10:00:03 +0000] "GET /api/products/7 HTTP/1.1" 200 128 "-" "Mozilla/5.0"
10.0.0.3 - - [07/Oct/2026:10:00:04 +0000] "POST /api/orders/9f1c2e4a-1b2c-4d5e-8f90-123456789abc/items HTTP/1.1" 201 64 "-" "curl"
`

func TestParseAccessLog(t *testing.T) {
	rec, err := ParseRecording([]byte(accessLog), Replay{Speed: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Arrivals) != 4 || rec.Skipped != 2 {
		t.Fatalf("arrivals %d skipped %d", len(rec.Arrivals), rec.Skipped)
	}
	var eps []string
	for _, e := range rec.Endpoints {
		eps = append(eps, e.Method+" "+e.Template)
	}
	if got := strings.Join(eps, ", "); got != "GET /api/products, GET /api/products/{id}, POST /api/orders/{uuid}/items" {
		t.Errorf("endpoints = %s", got)
	}
	// Speed 2 halves the gaps: 0, 0.5s, 1.5s, 2s.
	want := []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond, 2 * time.Second}
	for i, a := range rec.Arrivals {
		if a.At != want[i] {
			t.Errorf("arrival %d at %v, want %v", i, a.At, want[i])
		}
	}
	if rec.Arrivals[0].Target != "/api/products?page=1" || rec.Endpoints[1].Count != 2 {
		t.Errorf("arrival 0 %+v, endpoint 1 %+v", rec.Arrivals[0], rec.Endpoints[1])
	}
	if rates := rec.RatePerSecond(); len(rates) != 3 || rates[0] != 2 || rates[1] != 1 || rates[2] != 1 {
		t.Errorf("rates = %v", rates)
	}
	if rec, _ := ParseRecording([]byte(accessLog), Replay{Static: true, Limit: 2}); len(rec.Arrivals) != 2 || rec.Endpoints[len(rec.Endpoints)-1].Template != "/static/app.js" {
		t.Errorf("static kept and limit 2: %+v", rec)
	}
	if _, err := ParseRecording([]byte("no requests here\n"), Replay{}); err == nil {
		t.Error("an empty recording must fail")
	}
}

const har = `{"log": {"entries": [
 {"startedDateTime": "2026-10-07T10:00:00.000Z", "request": {"method": "GET", "url": "https://shop.example/api/cart"}},
 {"startedDateTime": "2026-10-07T10:00:00.250Z", "request": {"method": "GET", "url": "https://fonts.example/f.woff2"}},
 {"startedDateTime": "2026-10-07T10:00:00.500Z", "request": {"method": "POST", "url": "https://shop.example/api/cart", "postData": {"mimeType": "application/json", "text": "{\"id\":3}"}}},
 {"startedDateTime": "2026-10-07T10:00:01.000Z", "request": {"method": "GET", "url": "https://shop.example/api/orders/12?x=1"}}
]}}`

func TestParseHAR(t *testing.T) {
	rec, err := ParseRecording([]byte(har), Replay{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Arrivals) != 3 || rec.Skipped != 1 {
		t.Fatalf("arrivals %d skipped %d", len(rec.Arrivals), rec.Skipped)
	}
	post := rec.Arrivals[1]
	if post.Body != `{"id":3}` || post.ContentType != "application/json" || rec.Endpoints[post.Endpoint].Method != "POST" || !rec.Endpoints[post.Endpoint].HasBody {
		t.Errorf("post = %+v", post)
	}
	if rec.Arrivals[2].Target != "/api/orders/12?x=1" || rec.Arrivals[2].At != time.Second {
		t.Errorf("arrival 2 = %+v", rec.Arrivals[2])
	}
}

// TestReplaySpanLimit: a plan holds one rate per second of the recording,
// so a recording that spans centuries (or a tiny speed) must be refused
// rather than allocated or overflowed.
func TestReplaySpanLimit(t *testing.T) {
	far := `10.0.0.1 - - [07/Oct/1026:10:00:00 +0000] "GET /a HTTP/1.1" 200 1
10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] "GET /b HTTP/1.1" 200 1
`
	if _, err := ParseRecording([]byte(far), Replay{}); err == nil || !strings.Contains(err.Error(), "spans") {
		t.Errorf("a recording spanning 1000 years: %v", err)
	}
	day := `10.0.0.1 - - [07/Oct/2026:10:00:00 +0000] "GET /a HTTP/1.1" 200 1
10.0.0.1 - - [08/Oct/2026:10:00:00 +0000] "GET /b HTTP/1.1" 200 1
`
	if _, err := ParseRecording([]byte(day), Replay{Speed: 1e-12}); err == nil || !strings.Contains(err.Error(), "spans") {
		t.Errorf("a day at a tiny speed: %v", err)
	}
	rec, err := ParseRecording([]byte(day), Replay{Speed: 24})
	if err != nil || rec.Duration() != time.Hour {
		t.Errorf("a day at speed 24: %v %v", rec, err)
	}
}

func TestPathTemplate(t *testing.T) {
	for in, want := range map[string]string{
		"/":                "/",
		"/api/products/42": "/api/products/{id}",
		"/u/9f1c2e4a-1b2c-4d5e-8f90-123456789abc": "/u/{uuid}",
		"/blobs/0123456789abcdef0123":             "/blobs/{hex}",
		"/s/aB3dE5fG7hJ9kL1mN3pQ5rS7tU9":          "/s/{token}",
		"/docs/getting-started":                   "/docs/getting-started",
	} {
		if got := PathTemplate(in); got != want {
			t.Errorf("PathTemplate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadReplay(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "access.log"), []byte(accessLog), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
metadata: {name: replay}
target: {baseURL: "http://localhost:8080"}
load: {mode: replay, replay: {file: access.log, speed: 2}}
targets: ["http.p95 < 1s"]`
	if err := os.WriteFile(filepath.Join(dir, "s.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadFile(filepath.Join(dir, "s.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load.Plan(); err == nil {
		t.Error("the plan needs the recording")
	}
	prog, err := Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Journeys) != 3 || prog.Journeys[1].Name != "GET api.products.{id}" {
		t.Fatalf("journeys = %v", prog.Journeys)
	}
	plan, err := s.Load.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executor != ExecReplay || plan.Mode != ModeRate || plan.Peak() != 2 || plan.TotalDuration() != 3*time.Second {
		t.Errorf("plan = %+v", plan)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("a loaded replay scenario validates: %v", err)
	}

	for _, c := range []struct{ yaml, want string }{
		{`load: {mode: replay}`, "load.replay.file"},
		{`load: {mode: replay, replay: {file: a.log, format: csv}}`, "auto, log or har"},
		{`load: {mode: rate, rate: 1/s, duration: 1s, replay: {file: a.log}}`, "set load.mode to replay"},
	} {
		_, err := Parse([]byte("metadata: {name: r}\ntarget: {baseURL: \"http://x\"}\njourneys: [{name: a, steps: [{get: /}]}]\n" + c.yaml))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.yaml, err, c.want)
		}
	}
	if _, err := Parse([]byte("metadata: {name: r}\nload: {mode: replay, replay: {file: a.log}}")); err == nil || !strings.Contains(err.Error(), "target.baseURL") {
		t.Errorf("replay without a base URL: %v", err)
	}
}
