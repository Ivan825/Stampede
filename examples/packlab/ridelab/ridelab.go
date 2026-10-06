// Package ridelab is RideLab, an on-demand rides and food delivery
// service: riders see nearby cars, ask for an ETA and request a ride or
// order from a restaurant; a dispatcher matches each trip to a driver; the
// rider follows the driver live over a WebSocket until the trip is
// complete; drivers' apps stream their locations over a WebSocket. It is
// the reference app for the delivery pack.
//
// The fleet is 10,000 simulated drivers circling their neighbourhoods in
// a 15 km square city; positions are computed from the clock, so nothing
// runs in the background. Trips run thirty times faster than real time:
// a driver ten minutes away arrives in twenty seconds.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - geo: finding nearby drivers (for the map and for matching) checks
//     the distance to all 10,000 (fix "geo" looks only in the grid cells
//     around the point).
//   - dispatch: matching asks the routing service for the closest
//     candidates' ETAs to the pickup while holding the fleet lock, so trips
//     are matched one at a time and location updates wait (fix "dispatch"
//     asks first, then locks only to claim the chosen driver).
//   - eta: every ETA is a call to the routing service, which takes 5ms
//     and searches the city's 22,500 road blocks: every price check, every
//     match, and every tracking update to every rider, every second (fix
//     "eta" derives tracking ETAs from the route planned at matching and
//     caches travel times between blocks for 30 seconds).
package ridelab

import (
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Drivers is the size of the simulated fleet (d00001 ... d10000).
	Drivers = 10000
	// Riders is how many rider accounts exist (r0001 ... r5000).
	Riders = 5000
	// Password is every account's password.
	Password = "ridelab-pass"

	minLat, minLng = 12.90, 77.50
	span           = 0.135 // degrees: about 15 km
	gridN          = 50    // spatial index: 50 x 50 cells of 300 m
	roadN          = 150   // road grid: 150 x 150 blocks of 100 m
	orbit          = 0.003 // degrees a simulated driver circles within
	restaurants    = 300
)

type point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (p point) valid() bool {
	return p.Lat >= minLat && p.Lat <= minLat+span && p.Lng >= minLng && p.Lng <= minLng+span
}

// meters is the distance between two points (equirectangular; fine at
// city scale).
func meters(a, b point) float64 {
	x := (b.Lng - a.Lng) * math.Cos((a.Lat+b.Lat)/2*math.Pi/180)
	y := b.Lat - a.Lat
	return math.Sqrt(x*x+y*y) * 111_320
}

func cellOf(p point) int {
	r := min(gridN-1, max(0, int((p.Lat-minLat)/span*gridN)))
	c := min(gridN-1, max(0, int((p.Lng-minLng)/span*gridN)))
	return r*gridN + c
}

func blockOf(p point) int {
	r := min(roadN-1, max(0, int((p.Lat-minLat)/span*roadN)))
	c := min(roadN-1, max(0, int((p.Lng-minLng)/span*roadN)))
	return r*roadN + c
}

type driver struct {
	id     string
	center point
	phase  float64
	speed  float64 // radians a second

	// Guarded by the fleet lock.
	busy  bool
	trip  *trip
	live  bool // position comes from the driver's own app
	pos   point
	cell  int
	trips int
}

// at returns the driver's position at time t.
func (d *driver) at(t time.Time) point {
	if d.live {
		return d.pos
	}
	a := d.phase + d.speed*float64(t.UnixMilli())/1000
	return point{d.center.Lat + orbit*math.Sin(a), d.center.Lng + orbit*math.Cos(a)}
}

type trip struct {
	ID         string  `json:"tripId"`
	Type       string  `json:"type"`
	Pickup     point   `json:"pickup"`
	Dropoff    point   `json:"dropoff"`
	Restaurant string  `json:"restaurantId,omitempty"`
	Fare       float64 `json:"fare"`
	rider      string
	created    time.Time

	matched chan struct{} // closed when a driver is assigned

	mu       sync.Mutex
	driver   *driver
	from     point // driver's position when assigned
	assigned time.Time
	toPickup time.Duration
	ride     time.Duration
	canceled bool
	rating   int
}

type restaurant struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Cuisine  string  `json:"cuisine"`
	Location point   `json:"location"`
	Rating   float64 `json:"rating"`
}

type server struct {
	cfg   labkit.Config
	clock float64 // trip time runs this many times faster than real time
	tick  time.Duration
	delay time.Duration // dispatch delay before matching
	// routeCall is the routing service's round trip, on top of its search.
	routeCall time.Duration

	fleet   sync.Mutex
	drivers []*driver
	byID    map[string]*driver
	cells   [][]*driver // by orbit centre or live position (geo fix)

	roads [roadN * roadN]float64 // seconds to cross each block
	emu   sync.Mutex
	etas  map[[2]int]etaEntry

	rests []*restaurant

	tmu   sync.RWMutex
	trips map[string]*trip
	next  int

	smu      sync.RWMutex
	sessions map[string]string // token to rider or driver id

	examined labkit.Counter // drivers whose distance was checked
	routed   labkit.Counter // shortest-path searches
	locked   labkit.Counter // shortest-path searches run under the fleet lock
}

type etaEntry struct {
	secs float64
	at   time.Time
}

// New returns RideLab's handler.
func New(cfg labkit.Config) http.Handler {
	_, h := newServer(cfg)
	return h
}

var cuisines = []string{"Pizza", "Biryani", "Burgers", "Dosa", "Sushi", "Salads", "Noodles", "Bakery"}

func newServer(cfg labkit.Config) (*server, http.Handler) {
	s := &server{cfg: cfg, clock: 30, tick: time.Second, delay: 500 * time.Millisecond, routeCall: 5 * time.Millisecond, byID: map[string]*driver{},
		cells: make([][]*driver, gridN*gridN), etas: map[[2]int]etaEntry{}, trips: map[string]*trip{}, sessions: map[string]string{}}
	if cfg.Fast {
		s.clock, s.tick, s.delay, s.routeCall = 100, 50*time.Millisecond, 10*time.Millisecond, 50*time.Microsecond
	}
	// Deterministic pseudo-random layout.
	x := uint64(88172645463325252)
	rnd := func() float64 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return float64(x%1_000_000) / 1_000_000
	}
	for i := range s.roads {
		s.roads[i] = 8 + 10*rnd() // 100 m at 20 to 45 km/h
	}
	for i := range Drivers {
		d := &driver{id: fmt.Sprintf("d%05d", i+1), center: point{minLat + orbit + rnd()*(span-2*orbit), minLng + orbit + rnd()*(span-2*orbit)},
			phase: rnd() * 2 * math.Pi, speed: 0.002 + rnd()*0.004}
		d.cell = cellOf(d.center)
		s.drivers = append(s.drivers, d)
		s.byID[d.id] = d
		s.cells[d.cell] = append(s.cells[d.cell], d)
	}
	for i := range restaurants {
		s.rests = append(s.rests, &restaurant{ID: fmt.Sprintf("rest-%03d", i+1), Name: fmt.Sprintf("%s Place %d", cuisines[i%len(cuisines)], i+1),
			Cuisine: cuisines[i%len(cuisines)], Location: point{minLat + rnd()*span, minLng + rnd()*span}, Rating: 3.5 + float64(int(rnd()*15))/10})
	}
	routes := []labkit.Route{
		{Method: "POST", Path: "/api/login", Tag: "session", Summary: "Sign in as a rider (r0001) or a driver (d00001); returns a bearer token"},
		{Method: "GET", Path: "/api/restaurants", Tag: "restaurants", Summary: "Restaurants near lat,lng"},
		{Method: "GET", Path: "/api/restaurants/{id}/menu", Tag: "restaurants", Summary: "A restaurant's menu"},
		{Method: "GET", Path: "/api/drivers/nearby", Tag: "drivers", Summary: "Cars near lat,lng (the rider's map)"},
		{Method: "GET", Path: "/api/drivers/stream", Tag: "drivers", Summary: "WebSocket for a driver's app: send location updates"},
		{Method: "GET", Path: "/api/eta", Tag: "eta", Summary: "Travel time between two points (from, to as lat,lng)"},
		{Method: "POST", Path: "/api/trips", Tag: "trips", Summary: "Request a ride or a food delivery"},
		{Method: "GET", Path: "/api/trips/{id}", Tag: "trips", Summary: "A trip: searching, driver_assigned, arrived, in_progress, completed"},
		{Method: "POST", Path: "/api/trips/{id}/cancel", Tag: "trips", Summary: "Cancel a trip"},
		{Method: "POST", Path: "/api/trips/{id}/rating", Tag: "trips", Summary: "Rate a completed trip"},
		{Method: "GET", Path: "/api/trips/{id}/track", Tag: "tracking", Summary: "WebSocket: the driver's location and ETA until the trip completes"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "RideLab", "links": map[string]string{"openapi": "/openapi.json"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("RideLab", "On-demand rides and food delivery: matching, ETAs, live tracking and driver location streams, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/restaurants", s.auth(s.listRestaurants))
	mux.HandleFunc("GET /api/restaurants/{id}/menu", s.auth(s.menu))
	mux.HandleFunc("GET /api/drivers/nearby", s.auth(s.nearbyHandler))
	mux.HandleFunc("GET /api/drivers/stream", s.auth(s.driverStream))
	mux.HandleFunc("GET /api/eta", s.auth(s.etaHandler))
	mux.HandleFunc("POST /api/trips", s.auth(s.createTrip))
	mux.HandleFunc("GET /api/trips/{id}", s.auth(s.getTrip))
	mux.HandleFunc("POST /api/trips/{id}/cancel", s.auth(s.cancel))
	mux.HandleFunc("POST /api/trips/{id}/rating", s.auth(s.rate))
	mux.HandleFunc("GET /api/trips/{id}/track", s.auth(s.track))
	return s, mux
}

// --- sessions -------------------------------------------------------------

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	role := ""
	if n, err := strconv.Atoi(strings.TrimPrefix(in.ID, "r")); strings.HasPrefix(in.ID, "r") && err == nil && n >= 1 && n <= Riders && len(in.ID) == 5 {
		role = "rider"
	} else if s.byID[in.ID] != nil {
		role = "driver"
	}
	if role == "" || in.Password != Password {
		labkit.Error(w, 401, "invalid_credentials", "unknown rider or driver, or wrong password")
		return
	}
	tok := labkit.Token("rl_")
	s.smu.Lock()
	s.sessions[tok] = in.ID
	s.smu.Unlock()
	labkit.JSON(w, 200, map[string]any{"token": tok, "id": in.ID, "role": role})
}

func (s *server) auth(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.smu.RLock()
		id, ok := s.sessions[labkit.Bearer(r)]
		s.smu.RUnlock()
		if !ok {
			labkit.Error(w, 401, "unauthorized", "sign in at POST /api/login and send Authorization: Bearer <token>")
			return
		}
		next(w, r, id)
	}
}

func parsePoint(v string) (point, bool) {
	a, b, ok := strings.Cut(v, ",")
	lat, e1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	lng, e2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	p := point{lat, lng}
	return p, ok && e1 == nil && e2 == nil && p.valid()
}

func queryPoint(w http.ResponseWriter, r *http.Request) (point, bool) {
	q := r.URL.Query()
	p, ok := parsePoint(q.Get("lat") + "," + q.Get("lng"))
	if !ok {
		labkit.Error(w, 422, "bad_location", fmt.Sprintf("lat and lng inside the city (%.2f-%.3f, %.2f-%.3f)", minLat, minLat+span, minLng, minLng+span))
	}
	return p, ok
}

// --- the map --------------------------------------------------------------

type near struct {
	d    *driver
	pos  point
	dist float64
}

// nearby returns free drivers within radius metres of p, closest first.
// The caller holds the fleet lock.
func (s *server) nearby(p point, radius float64, now time.Time) []near {
	var out []near
	check := func(d *driver) {
		if d.busy {
			// A driver whose trip has ended is free, whether or not the
			// rider was still watching.
			if d.trip != nil {
				if st, _, _ := s.progress(d.trip, now); st != "completed" && st != "canceled" {
					return
				}
			}
			d.busy, d.trip = false, nil
		}
		pos := d.at(now)
		if m := meters(p, pos); m <= radius {
			out = append(out, near{d, pos, m})
		}
	}
	if s.cfg.Fixes.On("geo") {
		// Cells are 300 m; drivers circle up to 330 m from their cell.
		reach := int(math.Ceil((radius+400)/300)) + 1
		c := cellOf(p)
		r0, c0 := c/gridN, c%gridN
		n := 0
		for r := max(0, r0-reach); r <= min(gridN-1, r0+reach); r++ {
			for cc := max(0, c0-reach); cc <= min(gridN-1, c0+reach); cc++ {
				for _, d := range s.cells[r*gridN+cc] {
					check(d)
					n++
				}
			}
		}
		s.examined.Add(n)
	} else {
		// Bottleneck: check every driver in the city.
		for _, d := range s.drivers {
			check(d)
		}
		s.examined.Add(len(s.drivers))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dist < out[j].dist })
	return out
}

func (s *server) nearbyHandler(w http.ResponseWriter, r *http.Request, _ string) {
	p, ok := queryPoint(w, r)
	if !ok {
		return
	}
	s.fleet.Lock()
	ns := s.nearby(p, 1500, time.Now())
	s.fleet.Unlock()
	cars := make([]map[string]any, 0, 20)
	for _, n := range ns[:min(20, len(ns))] {
		cars = append(cars, map[string]any{"id": n.d.id, "lat": n.pos.Lat, "lng": n.pos.Lng, "meters": int(n.dist)})
	}
	labkit.JSON(w, 200, map[string]any{"count": len(ns), "cars": cars})
}

// --- routing ----------------------------------------------------------------

type node struct {
	c int
	d float64
}

type pq []node

func (q pq) Len() int           { return len(q) }
func (q pq) Less(i, j int) bool { return q[i].d < q[j].d }
func (q pq) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *pq) Push(x any)        { *q = append(*q, x.(node)) }
func (q *pq) Pop() any {
	old := *q
	x := old[len(old)-1]
	*q = old[:len(old)-1]
	return x
}

// route asks the routing service: Dijkstra over the road grid, from block
// to block, plus the call's round trip. It returns seconds of driving.
func (s *server) route(from, to int) float64 {
	s.routed.Add(1)
	time.Sleep(s.routeCall)
	dist := make([]float64, roadN*roadN)
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	dist[from] = 0
	q := &pq{{from, 0}}
	for q.Len() > 0 {
		cur := heap.Pop(q).(node)
		if cur.c == to {
			return cur.d
		}
		if cur.d > dist[cur.c] {
			continue
		}
		r, c := cur.c/roadN, cur.c%roadN
		for _, nb := range [4][2]int{{r - 1, c}, {r + 1, c}, {r, c - 1}, {r, c + 1}} {
			if nb[0] < 0 || nb[0] >= roadN || nb[1] < 0 || nb[1] >= roadN {
				continue
			}
			n := nb[0]*roadN + nb[1]
			if nd := cur.d + (s.roads[cur.c]+s.roads[n])/2; nd < dist[n] {
				dist[n] = nd
				heap.Push(q, node{n, nd})
			}
		}
	}
	return dist[to]
}

// stopTime is the minute every trip leg spends getting going and parking.
const stopTime = 60

// eta returns the driving time between two points in seconds.
func (s *server) eta(a, b point) float64 {
	from, to := blockOf(a), blockOf(b)
	if !s.cfg.Fixes.On("eta") {
		return stopTime + s.route(from, to) // bottleneck: route every time
	}
	k := [2]int{from, to}
	s.emu.Lock()
	e, ok := s.etas[k]
	s.emu.Unlock()
	if ok && time.Since(e.at) < 30*time.Second {
		return e.secs
	}
	secs := stopTime + s.route(from, to)
	s.emu.Lock()
	s.etas[k] = etaEntry{secs, time.Now()}
	s.emu.Unlock()
	return secs
}

func (s *server) etaHandler(w http.ResponseWriter, r *http.Request, _ string) {
	a, ok1 := parsePoint(r.URL.Query().Get("from"))
	b, ok2 := parsePoint(r.URL.Query().Get("to"))
	if !ok1 || !ok2 {
		labkit.Error(w, 422, "bad_location", "from and to are lat,lng inside the city")
		return
	}
	secs := s.eta(a, b)
	labkit.JSON(w, 200, map[string]any{"seconds": int(secs), "meters": int(meters(a, b)), "fare": fare(a, b)})
}

func fare(a, b point) float64 {
	return math.Round((40+meters(a, b)/1000*14)*100) / 100
}

// --- restaurants ------------------------------------------------------------

func (s *server) listRestaurants(w http.ResponseWriter, r *http.Request, _ string) {
	p, ok := queryPoint(w, r)
	if !ok {
		return
	}
	type rd struct {
		*restaurant
		Meters int `json:"meters"`
	}
	var out []rd
	for _, x := range s.rests {
		if m := meters(p, x.Location); m < 4000 {
			out = append(out, rd{x, int(m)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meters < out[j].Meters })
	labkit.JSON(w, 200, map[string]any{"restaurants": out[:min(20, len(out))]})
}

func (s *server) restaurant(id string) *restaurant {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "rest-"))
	if err != nil || n < 1 || n > len(s.rests) {
		return nil
	}
	return s.rests[n-1]
}

func (s *server) menu(w http.ResponseWriter, r *http.Request, _ string) {
	x := s.restaurant(r.PathValue("id"))
	if x == nil {
		labkit.Error(w, 404, "not_found", "no such restaurant")
		return
	}
	var items []map[string]any
	for i := range 10 {
		items = append(items, map[string]any{"id": fmt.Sprintf("%s-item-%d", x.ID, i+1), "name": fmt.Sprintf("%s dish %d", x.Cuisine, i+1), "price": 120 + 30*i})
	}
	labkit.JSON(w, 200, map[string]any{"restaurant": x, "items": items})
}

// --- trips --------------------------------------------------------------

func (s *server) createTrip(w http.ResponseWriter, r *http.Request, id string) {
	if !strings.HasPrefix(id, "r") {
		labkit.Error(w, 403, "riders_only", "drivers cannot request trips")
		return
	}
	var in struct {
		Type       string `json:"type"`
		Pickup     point  `json:"pickup"`
		Dropoff    point  `json:"dropoff"`
		Restaurant string `json:"restaurantId"`
		Items      []struct {
			ID  string `json:"id"`
			Qty int    `json:"qty"`
		} `json:"items"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	t := &trip{Type: in.Type, Pickup: in.Pickup, Dropoff: in.Dropoff, rider: id, created: time.Now(), matched: make(chan struct{})}
	switch in.Type {
	case "ride":
	case "food":
		x := s.restaurant(in.Restaurant)
		if x == nil || len(in.Items) == 0 {
			labkit.Error(w, 422, "bad_order", "a food order needs a restaurantId and items")
			return
		}
		t.Restaurant, t.Pickup = x.ID, x.Location
	default:
		labkit.Error(w, 422, "bad_type", "type is ride or food")
		return
	}
	if !t.Pickup.valid() || !t.Dropoff.valid() {
		labkit.Error(w, 422, "bad_location", "pickup and dropoff must be inside the city")
		return
	}
	t.Fare = fare(t.Pickup, t.Dropoff)
	s.tmu.Lock()
	s.next++
	t.ID = fmt.Sprintf("trip_%07d", s.next)
	s.trips[t.ID] = t
	s.tmu.Unlock()
	go func() {
		time.Sleep(s.delay)
		s.match(t)
	}()
	labkit.JSON(w, 201, map[string]any{"tripId": t.ID, "status": "searching", "fare": t.Fare})
}

// match assigns the best of the closest free drivers, by driving time to
// the pickup.
func (s *server) match(t *trip) {
	for radius := 1500.0; radius <= 12000; radius *= 2 {
		if s.cfg.Fixes.On("dispatch") {
			now := time.Now()
			s.fleet.Lock()
			cands := s.nearby(t.Pickup, radius, now)
			s.fleet.Unlock()
			cands = cands[:min(5, len(cands))]
			etas := make([]float64, len(cands))
			for i, c := range cands {
				etas[i] = s.eta(c.pos, t.Pickup)
			}
			ride := s.eta(t.Pickup, t.Dropoff)
			order := make([]int, len(cands))
			for i := range order {
				order[i] = i
			}
			sort.Slice(order, func(i, j int) bool { return etas[order[i]] < etas[order[j]] })
			s.fleet.Lock()
			for _, i := range order {
				if d := cands[i].d; !d.busy {
					s.assign(t, d, cands[i].pos, etas[i], ride, now)
					s.fleet.Unlock()
					return
				}
			}
			s.fleet.Unlock()
			continue
		}
		// Bottleneck: route the candidates while holding the fleet lock.
		s.fleet.Lock()
		now := time.Now()
		cands := s.nearby(t.Pickup, radius, now)
		best, bestETA := -1, math.Inf(1)
		for i, c := range cands[:min(5, len(cands))] {
			s.locked.Add(1)
			if e := s.eta(c.pos, t.Pickup); e < bestETA {
				best, bestETA = i, e
			}
		}
		if best >= 0 {
			s.locked.Add(1)
			s.assign(t, cands[best].d, cands[best].pos, bestETA, s.eta(t.Pickup, t.Dropoff), now)
		}
		s.fleet.Unlock()
		if best >= 0 {
			return
		}
	}
}

// assign gives the trip to a driver; the caller holds the fleet lock.
// eta and ride are seconds of driving to the pickup and of the trip.
func (s *server) assign(t *trip, d *driver, pos point, eta, ride float64, now time.Time) {
	d.busy, d.trip = true, t
	d.trips++
	t.mu.Lock()
	t.driver, t.from, t.assigned = d, pos, now
	// Trip time runs on a compressed clock.
	t.toPickup = time.Duration(eta / s.clock * float64(time.Second))
	t.ride = time.Duration(ride / s.clock * float64(time.Second))
	if t.Type == "food" {
		t.ride += time.Duration(180 / s.clock * float64(time.Second)) // three minutes' cooking, compressed
	}
	t.mu.Unlock()
	close(t.matched)
}

// release frees the trip's driver if they are still on it.
func (s *server) release(t *trip) {
	t.mu.Lock()
	d := t.driver
	t.mu.Unlock()
	if d == nil {
		return
	}
	s.fleet.Lock()
	if d.trip == t {
		d.busy, d.trip = false, nil
	}
	s.fleet.Unlock()
}

const arrivedFor = 60 // seconds the driver waits at the pickup

// state returns the trip's status, the driver's position and the seconds
// left to the next stop, at time now.
func (s *server) state(t *trip, now time.Time) (string, point, float64) {
	st, pos, left := s.progress(t, now)
	if st == "completed" {
		s.release(t)
	}
	return st, pos, left
}

func (s *server) progress(t *trip, now time.Time) (string, point, float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.canceled:
		return "canceled", point{}, 0
	case t.driver == nil:
		return "searching", point{}, 0
	}
	since := now.Sub(t.assigned)
	wait := time.Duration(arrivedFor / s.clock * float64(time.Second))
	lerp := func(a, b point, f float64) point {
		f = min(1, max(0, f))
		return point{a.Lat + (b.Lat-a.Lat)*f, a.Lng + (b.Lng-a.Lng)*f}
	}
	switch {
	case since < t.toPickup:
		f := float64(since) / float64(t.toPickup)
		return "driver_assigned", lerp(t.from, t.Pickup, f), (t.toPickup - since).Seconds() * s.clock
	case since < t.toPickup+wait:
		return "arrived", t.Pickup, 0
	case since < t.toPickup+wait+t.ride:
		f := float64(since-t.toPickup-wait) / float64(t.ride)
		return "in_progress", lerp(t.Pickup, t.Dropoff, f), (t.toPickup + wait + t.ride - since).Seconds() * s.clock
	}
	return "completed", t.Dropoff, 0
}

func (s *server) trip(w http.ResponseWriter, r *http.Request, id string) *trip {
	s.tmu.RLock()
	t := s.trips[r.PathValue("id")]
	s.tmu.RUnlock()
	if t == nil || t.rider != id {
		labkit.Error(w, 404, "not_found", "no such trip")
		return nil
	}
	return t
}

func (s *server) view(t *trip) map[string]any {
	st, pos, left := s.state(t, time.Now())
	out := map[string]any{"tripId": t.ID, "type": t.Type, "status": st, "pickup": t.Pickup, "dropoff": t.Dropoff, "fare": t.Fare}
	if t.Restaurant != "" {
		out["restaurantId"] = t.Restaurant
	}
	t.mu.Lock()
	if t.driver != nil && st != "canceled" {
		out["driver"] = map[string]any{"id": t.driver.id, "location": pos}
		out["etaSeconds"] = int(left)
	}
	if t.rating > 0 {
		out["rating"] = t.rating
	}
	t.mu.Unlock()
	return out
}

func (s *server) getTrip(w http.ResponseWriter, r *http.Request, id string) {
	if t := s.trip(w, r, id); t != nil {
		labkit.JSON(w, 200, s.view(t))
	}
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request, id string) {
	t := s.trip(w, r, id)
	if t == nil {
		return
	}
	st, _, _ := s.state(t, time.Now())
	if st == "in_progress" || st == "completed" {
		labkit.Error(w, 409, "too_late", "the trip has started")
		return
	}
	t.mu.Lock()
	t.canceled = true
	t.mu.Unlock()
	s.release(t)
	labkit.JSON(w, 200, map[string]any{"tripId": t.ID, "status": "canceled"})
}

func (s *server) rate(w http.ResponseWriter, r *http.Request, id string) {
	t := s.trip(w, r, id)
	if t == nil {
		return
	}
	var in struct {
		Stars int `json:"stars"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if st, _, _ := s.state(t, time.Now()); st != "completed" || in.Stars < 1 || in.Stars > 5 {
		labkit.Error(w, 409, "cannot_rate", "rate a completed trip with 1 to 5 stars")
		return
	}
	t.mu.Lock()
	t.rating = in.Stars
	t.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"tripId": t.ID, "rating": in.Stars})
}

// --- WebSockets -----------------------------------------------------------

// track streams the driver's position and ETA every tick, and each status
// change, until the trip completes or the rider leaves.
func (s *server) track(w http.ResponseWriter, r *http.Request, id string) {
	t := s.trip(w, r, id)
	if t == nil {
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ctx := conn.CloseRead(r.Context())
	send := func(v any) bool {
		b, _ := json.Marshal(v)
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, b) == nil
	}
	tk := time.NewTicker(s.tick)
	defer tk.Stop()
	matched := t.matched // tell the rider the moment a driver is assigned
	last := ""
	for {
		st, pos, left := s.state(t, time.Now())
		if st != last {
			if !send(map[string]any{"type": "status", "status": st}) {
				return
			}
			last = st
		}
		if st == "completed" || st == "canceled" {
			<-ctx.Done()
			return
		}
		if st == "driver_assigned" || st == "in_progress" {
			eta := left // the trip's plan, routed once when it was matched
			if !s.cfg.Fixes.On("eta") {
				// Bottleneck: route again from where the driver is now,
				// every tick, for every rider watching.
				target := t.Pickup
				if st == "in_progress" {
					target = t.Dropoff
				}
				eta = s.eta(pos, target)
			}
			if !send(map[string]any{"type": "driver_location", "lat": pos.Lat, "lng": pos.Lng, "etaSeconds": int(eta)}) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		case <-matched:
			matched = nil
		}
	}
}

// driverStream takes a driver app's location updates. A driver who
// streams locations is placed where they say, instead of the simulation.
func (s *server) driverStream(w http.ResponseWriter, r *http.Request, id string) {
	d := s.byID[id]
	if d == nil {
		labkit.Error(w, 403, "drivers_only", "sign in as a driver")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 10)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ctx := r.Context()
	n := 0
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var in struct {
			Type string  `json:"type"`
			Lat  float64 `json:"lat"`
			Lng  float64 `json:"lng"`
		}
		reply := map[string]any{"type": "ack"}
		err = json.Unmarshal(data, &in)
		p := point{in.Lat, in.Lng}
		if err != nil || in.Type != "location" {
			reply = map[string]any{"type": "error", "error": "send {type: location, lat, lng}"}
		} else if !p.valid() {
			reply = map[string]any{"type": "error", "error": "location outside the city"}
		} else {
			s.fleet.Lock()
			s.move(d, p)
			s.fleet.Unlock()
			n++
			reply["seq"] = n
			reply["cell"] = cellOf(p)
		}
		b, _ := json.Marshal(reply)
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = conn.Write(wctx, websocket.MessageText, b)
		cancel()
		if err != nil {
			return
		}
	}
}

// move places a live driver; the caller holds the fleet lock.
func (s *server) move(d *driver, p point) {
	c := cellOf(p)
	if !d.live || c != d.cell {
		old := s.cells[d.cell]
		for i, x := range old {
			if x == d {
				s.cells[d.cell] = append(old[:i:i], old[i+1:]...)
				break
			}
		}
		s.cells[c] = append(s.cells[c], d)
		d.cell = c
	}
	d.live, d.pos = true, p
}
