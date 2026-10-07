package engine

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestBaggageNamesJourneyAndStep(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("baggage"))
		mu.Unlock()
	}))
	defer srv.Close()
	run(t, fmt.Sprintf(`
metadata: {name: bag}
target: {baseURL: %q}
journeys: [{name: "check out", steps: [{name: "pay, now", get: /pay}]}]
load: {iterations: 1}`, srv.URL), func(o *Options) { o.RunID = "r1" })
	want := "stampede.run_id=r1,stampede.vu=0,stampede.journey=check%20out,stampede.step=pay%2C%20now"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("baggage %q, want %q", got, want)
	}
}
