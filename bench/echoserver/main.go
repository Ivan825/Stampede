// Command echoserver is the calibrated target used by Stampede's accuracy
// benchmark. Each response is delayed by a value drawn from a known
// distribution, and the server records exactly how long it held every
// request, so a load generator's reported percentiles can be compared
// with ground truth.
//
//	echoserver -addr :9099 -dist lognormal:20ms:0.5
//	curl localhost:9099/stats      # exact server-side percentiles
//	curl -X POST localhost:9099/reset
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type dist func(r *rand.Rand) time.Duration

func parseDist(s string) (dist, error) {
	parts := strings.Split(s, ":")
	dur := func(i int) (time.Duration, error) {
		if i >= len(parts) {
			return 0, fmt.Errorf("dist %q: missing parameter %d", s, i)
		}
		return time.ParseDuration(parts[i])
	}
	switch parts[0] {
	case "fixed":
		d, err := dur(1)
		return func(*rand.Rand) time.Duration { return d }, err
	case "uniform":
		lo, err := dur(1)
		if err != nil {
			return nil, err
		}
		hi, err := dur(2)
		return func(r *rand.Rand) time.Duration { return lo + time.Duration(r.Int64N(int64(hi-lo)+1)) }, err
	case "lognormal":
		median, err := dur(1)
		if err != nil {
			return nil, err
		}
		var sigma float64
		if len(parts) > 2 {
			_, err = fmt.Sscanf(parts[2], "%g", &sigma)
		}
		return func(r *rand.Rand) time.Duration {
			return time.Duration(float64(median) * math.Exp(r.NormFloat64()*sigma))
		}, err
	case "bimodal":
		// bimodal:fast:slow:slowFraction
		fast, err := dur(1)
		if err != nil {
			return nil, err
		}
		slow, err := dur(2)
		if err != nil {
			return nil, err
		}
		var frac float64
		if len(parts) > 3 {
			_, err = fmt.Sscanf(parts[3], "%g", &frac)
		}
		return func(r *rand.Rand) time.Duration {
			if r.Float64() < frac {
				return slow
			}
			return fast
		}, err
	}
	return nil, fmt.Errorf("unknown dist %q (fixed, uniform, lognormal, bimodal)", s)
}

type recorder struct {
	mu   sync.Mutex
	held []time.Duration
}

func (rc *recorder) add(d time.Duration) {
	rc.mu.Lock()
	rc.held = append(rc.held, d)
	rc.mu.Unlock()
}

// Stats are exact percentiles of how long the server held requests.
type Stats struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	P999  float64 `json:"p999"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
}

func (rc *recorder) stats() Stats {
	rc.mu.Lock()
	v := slices.Clone(rc.held)
	rc.mu.Unlock()
	slices.Sort(v)
	s := Stats{Count: len(v)}
	if len(v) == 0 {
		return s
	}
	q := func(p float64) float64 {
		i := int(math.Ceil(p*float64(len(v)))) - 1
		return v[max(i, 0)].Seconds()
	}
	var sum time.Duration
	for _, d := range v {
		sum += d
	}
	s.P50, s.P90, s.P95, s.P99, s.P999 = q(.5), q(.9), q(.95), q(.99), q(.999)
	s.Max, s.Mean = v[len(v)-1].Seconds(), (sum / time.Duration(len(v))).Seconds()
	return s
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9099", "listen address")
	ds := flag.String("dist", "fixed:10ms", "delay distribution")
	seed := flag.Uint64("seed", 1, "random seed")
	flag.Parse()
	d, err := parseDist(*ds)
	if err != nil {
		log.Fatal(err)
	}
	var rmu sync.Mutex
	rng := rand.New(rand.NewPCG(*seed, *seed+1))
	rec := &recorder{}

	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec.stats())
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, _ *http.Request) {
		rec.mu.Lock()
		rec.held = rec.held[:0]
		rec.mu.Unlock()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rmu.Lock()
		delay := d(rng)
		rmu.Unlock()
		time.Sleep(delay - time.Since(start))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ok":true,"items":[{"id":1}],"path":%q}`, r.URL.Path)
		rec.add(time.Since(start))
	})
	log.Printf("echoserver listening on %s with %s", *addr, *ds)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
