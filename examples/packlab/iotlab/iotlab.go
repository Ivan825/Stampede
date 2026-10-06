// Package iotlab is IoTLab, an IoT platform: an MQTT broker that devices
// connect to and publish readings through, an ingest service that stores
// every reading and acknowledges it to its device, device shadows (a
// desired state set over HTTP and fetched or pushed over MQTT), and a
// device registry and telemetry store over HTTP. It is the reference app
// for the iot pack.
//
// Planted bottlenecks (see README.md), each switched off by a fix flag:
//
//   - ingest: one worker stores every reading in turn, so when the fleet
//     reports faster than one write at a time keeps up, readings queue and
//     acknowledgements fall behind (fix "ingest" runs 16 workers, each
//     owning a share of the devices so a device's readings stay in order).
//   - presence: every connect and disconnect recounts the whole fleet's
//     online summary while holding the registry lock that sign-ins also
//     need, so a reconnect storm queues behind itself (fix "presence"
//     keeps the counts up to date as devices come and go).
package iotlab

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/Ivan825/Stampede/examples/packlab/labkit"
)

const (
	// Devices is the fleet: dev-0 to dev-19999, one per virtual user
	// (vu counts from 0).
	Devices = 20000
	// Password is every device's MQTT password.
	Password = "iotlab-pass"
	// keep is how many readings a device's history holds.
	keep = 100
)

var models = []string{"thermostat", "meter", "tracker", "camera", "gateway"}

type reading struct {
	Seq  json.RawMessage `json:"seq"`
	At   string          `json:"at"`
	Data json.RawMessage `json:"data"`
}

type device struct {
	id, model, site string

	// Guarded by server.mu.
	online   bool
	conn     *mochi.Client
	lastSeen time.Time

	mu       sync.Mutex
	readings []reading
	total    int64
	version  int
	desired  map[string]any
}

type summary struct {
	Online  int            `json:"online"`
	ByModel map[string]int `json:"onlineByModel"`
	BySite  map[string]int `json:"onlineBySite"`
}

type message struct {
	dev     *device
	payload []byte
	at      time.Time
}

type server struct {
	cfg    labkit.Config
	broker *mochi.Server
	list   []*device
	byID   map[string]*device

	// mu is the registry lock: presence changes write under it, sign-ins
	// and fleet reads read under it.
	mu      sync.RWMutex
	sum     summary
	scanned labkit.Counter

	queues   []chan message
	done     chan struct{}
	queued   atomic.Int64
	ingested atomic.Int64
	writing  labkit.Gauge
	write    time.Duration
}

// Open starts IoTLab's broker on cfg.Listen and returns the app.
func Open(cfg labkit.Config) (*labkit.App, error) {
	_, app, err := newServer(cfg)
	return app, err
}

func newServer(cfg labkit.Config) (*server, *labkit.App, error) {
	s := &server{cfg: cfg, byID: map[string]*device{}, done: make(chan struct{}), write: time.Millisecond,
		sum: summary{ByModel: map[string]int{}, BySite: map[string]int{}}}
	if cfg.Fast {
		s.write = 100 * time.Microsecond
	}
	for i := range Devices {
		d := &device{id: "dev-" + strconv.Itoa(i), model: models[i%len(models)], site: fmt.Sprintf("site-%02d", i%50+1)}
		s.list = append(s.list, d)
		s.byID[d.id] = d
	}

	s.broker = mochi.New(&mochi.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.broker.AddHook(&hook{s: s}, nil); err != nil {
		return nil, nil, err
	}
	addr := cfg.Listen
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	if err := s.broker.AddListener(listeners.NewNet("devices", ln)); err != nil {
		ln.Close()
		return nil, nil, err
	}
	if err := s.broker.Serve(); err != nil {
		return nil, nil, err
	}
	if err := s.broker.Subscribe("devices/+/telemetry", 1, s.receive); err != nil {
		_ = s.broker.Close()
		return nil, nil, err
	}
	if err := s.broker.Subscribe("devices/+/shadow/get", 2, s.shadowGet); err != nil {
		_ = s.broker.Close()
		return nil, nil, err
	}
	workers := 1
	if cfg.Fixes.On("ingest") {
		workers = 16
	}
	for range workers {
		q := make(chan message, 10000)
		s.queues = append(s.queues, q)
		go s.ingest(q)
	}

	routes := []labkit.Route{
		{Method: "GET", Path: "/api/fleet", Tag: "fleet", Summary: "How many devices are online, by model and site"},
		{Method: "GET", Path: "/api/devices", Tag: "devices", Summary: "List devices (limit, offset)"},
		{Method: "GET", Path: "/api/devices/{id}", Tag: "devices", Summary: "One device and its last reading"},
		{Method: "GET", Path: "/api/devices/{id}/telemetry", Tag: "telemetry", Summary: "A device's recent readings"},
		{Method: "GET", Path: "/api/devices/{id}/shadow", Tag: "shadows", Summary: "A device's desired state"},
		{Method: "PUT", Path: "/api/devices/{id}/shadow", Tag: "shadows", Summary: "Set a device's desired state (pushed over MQTT when it is connected)"},
		{Method: "GET", Path: "/api/ingest", Tag: "telemetry", Summary: "Readings waiting to be stored, and stored so far"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"name": "IoTLab", "mqtt": "tcp://" + labkit.Dialable(ln.Addr()),
			"links": map[string]string{"openapi": "/openapi.json", "fleet": "/api/fleet"}})
	})
	mux.Handle("GET /openapi.json", labkit.OpenAPI("IoTLab", "A device fleet over MQTT with telemetry ingest, with planted bottlenecks.", routes))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { labkit.JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/fleet", s.fleet)
	mux.HandleFunc("GET /api/devices", s.devices)
	mux.HandleFunc("GET /api/devices/{id}", s.device)
	mux.HandleFunc("GET /api/devices/{id}/telemetry", s.telemetry)
	mux.HandleFunc("GET /api/devices/{id}/shadow", s.shadow)
	mux.HandleFunc("PUT /api/devices/{id}/shadow", s.setShadow)
	mux.HandleFunc("GET /api/ingest", func(w http.ResponseWriter, _ *http.Request) {
		labkit.JSON(w, 200, map[string]any{"queued": s.queued.Load(), "ingested": s.ingested.Load(), "workers": len(s.queues)})
	})
	return s, &labkit.App{
		Handler: mux,
		Env:     map[string]string{"MQTT_BROKER": "tcp://" + labkit.Dialable(ln.Addr())},
		Close: func() {
			close(s.done)
			_ = s.broker.Close()
		},
	}, nil
}

// hook signs devices in, keeps each to its own topics and tracks who is
// online.
type hook struct {
	mochi.HookBase
	s *server
}

func (h *hook) ID() string { return "iotlab-fleet" }

func (h *hook) Provides(b byte) bool {
	return bytes.Contains([]byte{mochi.OnConnectAuthenticate, mochi.OnACLCheck, mochi.OnSessionEstablished, mochi.OnDisconnect}, []byte{b})
}

// OnConnectAuthenticate accepts a registered device whose client id is
// its device id, with the fleet password.
func (h *hook) OnConnectAuthenticate(cl *mochi.Client, pk packets.Packet) bool {
	// The registry is shared with presence updates, so sign-ins read it
	// under its lock.
	h.s.mu.RLock()
	d := h.s.byID[string(pk.Connect.Username)]
	h.s.mu.RUnlock()
	return d != nil && cl.ID == d.id && subtle.ConstantTimeCompare(pk.Connect.Password, []byte(Password)) == 1
}

// OnACLCheck lets a device publish and subscribe only under
// devices/<its id>/.
func (h *hook) OnACLCheck(cl *mochi.Client, topic string, _ bool) bool {
	return strings.HasPrefix(topic, "devices/"+cl.ID+"/")
}

func (h *hook) OnSessionEstablished(cl *mochi.Client, _ packets.Packet) { h.s.presence(cl, true) }

func (h *hook) OnDisconnect(cl *mochi.Client, _ error, _ bool) { h.s.presence(cl, false) }

func (s *server) presence(cl *mochi.Client, online bool) {
	d := s.byID[cl.ID]
	if d == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case online:
		d.conn = cl
	case d.conn != cl:
		return // an older connection of a device that has reconnected
	default:
		d.conn = nil
	}
	was := d.online
	d.online, d.lastSeen = online, time.Now()
	if s.cfg.Fixes.On("presence") {
		if was != online {
			n := 1
			if !online {
				n = -1
			}
			s.sum.Online += n
			s.sum.ByModel[d.model] += n
			s.sum.BySite[d.site] += n
		}
		return
	}
	// Bottleneck: recount the whole fleet on every change, under the lock.
	sum := summary{ByModel: map[string]int{}, BySite: map[string]int{}}
	for _, x := range s.list {
		if x.online {
			sum.Online++
			sum.ByModel[x.model]++
			sum.BySite[x.site]++
		}
	}
	s.scanned.Add(len(s.list))
	s.sum = sum
}

// receive takes a reading off the broker and queues it for ingest. It
// runs on the publishing device's connection, so a full queue slows the
// device down.
func (s *server) receive(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
	id := strings.TrimSuffix(strings.TrimPrefix(pk.TopicName, "devices/"), "/telemetry")
	d := s.byID[id]
	if d == nil {
		return
	}
	q := s.queues[0]
	if len(s.queues) > 1 {
		h := fnv.New32a()
		h.Write([]byte(id))
		q = s.queues[h.Sum32()%uint32(len(s.queues))]
	}
	s.queued.Add(1)
	select {
	case q <- message{dev: d, payload: bytes.Clone(pk.Payload), at: time.Now()}:
	case <-s.done:
	}
}

func (s *server) ingest(q chan message) {
	for {
		var m message
		select {
		case m = <-q:
		case <-s.done:
			return
		}
		s.queued.Add(-1)
		var in struct {
			Seq json.RawMessage `json:"seq"`
		}
		var ack any
		if json.Unmarshal(m.payload, &in) != nil || len(in.Seq) == 0 {
			ack = map[string]string{"error": `a reading must be a JSON object with a "seq"`}
		} else {
			// The time-series store's write.
			end := s.writing.Enter()
			time.Sleep(s.write)
			end()
			now := time.Now()
			m.dev.mu.Lock()
			m.dev.readings = append(m.dev.readings, reading{Seq: in.Seq, At: now.UTC().Format(time.RFC3339Nano), Data: m.payload})
			if len(m.dev.readings) > 2*keep {
				m.dev.readings = append(m.dev.readings[:0:0], m.dev.readings[len(m.dev.readings)-keep:]...)
			}
			m.dev.total++
			m.dev.mu.Unlock()
			s.ingested.Add(1)
			ack = map[string]any{"seq": in.Seq, "queuedMs": now.Sub(m.at).Milliseconds()}
		}
		b, _ := json.Marshal(ack)
		_ = s.broker.Publish("devices/"+m.dev.id+"/acks", b, false, 1)
	}
}

func (s *server) fleet(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	labkit.JSON(w, 200, map[string]any{"devices": len(s.list), "online": s.sum.Online, "onlineByModel": s.sum.ByModel, "onlineBySite": s.sum.BySite})
}

func (s *server) view(d *device) map[string]any {
	s.mu.RLock()
	v := map[string]any{"id": d.id, "model": d.model, "site": d.site, "online": d.online}
	if !d.lastSeen.IsZero() {
		v["lastSeen"] = d.lastSeen.UTC().Format(time.RFC3339Nano)
	}
	s.mu.RUnlock()
	return v
}

func (s *server) devices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = min(max(offset, 0), len(s.list))
	out := []map[string]any{}
	for _, d := range s.list[offset:min(offset+limit, len(s.list))] {
		out = append(out, s.view(d))
	}
	labkit.JSON(w, 200, map[string]any{"devices": out, "total": len(s.list)})
}

func (s *server) lookup(w http.ResponseWriter, r *http.Request) *device {
	d := s.byID[r.PathValue("id")]
	if d == nil {
		labkit.Error(w, 404, "not_found", "no such device")
	}
	return d
}

func (s *server) device(w http.ResponseWriter, r *http.Request) {
	d := s.lookup(w, r)
	if d == nil {
		return
	}
	v := s.view(d)
	d.mu.Lock()
	v["readings"] = d.total
	if n := len(d.readings); n > 0 {
		v["last"] = d.readings[n-1]
	}
	d.mu.Unlock()
	labkit.JSON(w, 200, v)
}

func (s *server) telemetry(w http.ResponseWriter, r *http.Request) {
	d := s.lookup(w, r)
	if d == nil {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > keep {
		limit = 20
	}
	d.mu.Lock()
	out := append([]reading{}, d.readings[max(0, len(d.readings)-limit):]...)
	d.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"device": d.id, "readings": out})
}

// shadowGet answers a device's request for its desired state, sent to
// devices/<id>/shadow/get, on devices/<id>/shadow with the request's
// token.
func (s *server) shadowGet(_ *mochi.Client, _ packets.Subscription, pk packets.Packet) {
	id := strings.TrimSuffix(strings.TrimPrefix(pk.TopicName, "devices/"), "/shadow/get")
	d := s.byID[id]
	if d == nil {
		return
	}
	var in struct {
		Token json.RawMessage `json:"token"`
	}
	_ = json.Unmarshal(pk.Payload, &in)
	d.mu.Lock()
	b, _ := json.Marshal(map[string]any{"token": in.Token, "version": d.version, "desired": d.desired})
	d.mu.Unlock()
	// Publishing from inside the broker's delivery would re-enter it.
	go func() { _ = s.broker.Publish("devices/"+id+"/shadow", b, false, 1) }()
}

func (s *server) shadow(w http.ResponseWriter, r *http.Request) {
	d := s.lookup(w, r)
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	labkit.JSON(w, 200, map[string]any{"device": d.id, "version": d.version, "desired": d.desired})
}

// setShadow stores a new desired state and, when the device is
// connected, pushes it on devices/<id>/shadow.
func (s *server) setShadow(w http.ResponseWriter, r *http.Request) {
	d := s.lookup(w, r)
	if d == nil {
		return
	}
	var in struct {
		Desired map[string]any `json:"desired"`
	}
	if !labkit.Decode(w, r, &in) {
		return
	}
	if in.Desired == nil {
		labkit.Error(w, 400, "invalid_shadow", "desired is required")
		return
	}
	d.mu.Lock()
	d.version++
	d.desired = in.Desired
	version := d.version
	d.mu.Unlock()
	s.mu.RLock()
	online := d.online
	s.mu.RUnlock()
	if online {
		b, _ := json.Marshal(map[string]any{"version": version, "desired": in.Desired})
		if err := s.broker.Publish("devices/"+d.id+"/shadow", b, false, 1); err != nil {
			labkit.Error(w, 502, "publish_failed", err.Error())
			return
		}
	}
	labkit.JSON(w, 200, map[string]any{"device": d.id, "version": version, "pushed": online})
}
