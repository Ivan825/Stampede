package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/tidwall/gjson"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

const version = "0.1.0"

// queueSize bounds the messages a user keeps for later expect steps;
// when it is full the oldest is dropped.
const queueSize = 1000

const connectSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["broker"],
  "properties": {
    "broker": {"type": "string", "x-stampede-target": true, "description": "Broker URL: tcp://, mqtt://, ssl://, mqtts://, ws:// or wss://host:port."},
    "clientId": {"type": "string", "description": "Client id (default stampede-<run>-<vu>); must be unique per user."},
    "username": {"type": "string"},
    "password": {"type": "string"},
    "cleanSession": {"type": "boolean", "description": "Start a clean session (default true)."},
    "keepAlive": {"type": "string", "description": "Keep-alive interval such as 30s (default 30s)."},
    "insecureSkipVerify": {"type": "boolean", "description": "Skip TLS certificate checks (test brokers only)."},
    "fresh": {"type": "boolean", "description": "Reconnect even when already connected (default false: an open connection is reused and the step is skipped)."}
  }
}`

const publishSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["topic"],
  "properties": {
    "topic": {"type": "string", "minLength": 1},
    "payload": {"type": "string", "description": "Message payload (text)."},
    "qos": {"enum": [0, 1, 2], "description": "0 (default), 1 or 2; latency waits for PUBACK or PUBCOMP."},
    "retain": {"type": "boolean"}
  }
}`

const subscribeSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["topic"],
  "properties": {
    "topic": {"type": "string", "minLength": 1, "description": "Topic filter; + and # wildcards allowed."},
    "qos": {"enum": [0, 1, 2]}
  }
}`

const expectSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "topic": {"type": "string", "description": "Only messages on topics matching this filter (default: any subscribed topic)."},
    "match": {"type": "string", "description": "Regex the payload must match."},
    "json": {"type": "object", "description": "JSONPath ($.a.b) to the expected value, or \"exists\"."},
    "count": {"type": "integer", "minimum": 1, "description": "How many matching messages to wait for (default 1)."}
  }
}`

func newPlugin() *pluginsdk.Plugin {
	return &pluginsdk.Plugin{
		Name:        "mqtt",
		Version:     version,
		Description: "Each virtual user is an MQTT 3.1.1 client: connect, publish, subscribe, expect.",
		NewSession: func(_ context.Context, info pluginsdk.SessionInfo) (any, error) {
			return &session{vu: info.VU, run: info.RunID, notify: make(chan struct{}, 1)}, nil
		},
		Steps: []pluginsdk.Step{
			{Name: "connect", Description: "Connect the user's client to a broker (skipped when already connected).", Schema: connectSchema, Run: connect},
			{Name: "publish", Description: "Publish a message; with QoS 1 or 2 the latency includes the broker's acknowledgement.", Schema: publishSchema, Run: publish},
			{Name: "subscribe", Description: "Subscribe to a topic filter; messages are kept for expect steps.", Schema: subscribeSchema, Run: subscribe},
			{Name: "expect", Description: "Wait for matching messages; latency runs from the user's last publish or subscribe.", Schema: expectSchema, Run: expect},
			{Name: "disconnect", Description: "Disconnect the user's client.", Schema: `{"type": "object", "additionalProperties": false, "properties": {}}`, Run: disconnect},
		},
	}
}

type message struct {
	topic   string
	payload []byte
	at      time.Time
}

// session is one user's MQTT client and the messages it has received.
type session struct {
	vu  int64
	run string

	client paho.Client
	key    string // broker and client id of the connection
	subs   map[string]bool
	// mark is the user's last publish or subscribe: expect latency runs
	// from it.
	mark time.Time

	mu      sync.Mutex
	queue   []message
	dropped int64
	notify  chan struct{}
}

func (s *session) Close() error { //nolint:unparam // implements io.Closer
	if s.client != nil {
		s.client.Disconnect(100)
	}
	return nil
}

func (s *session) onMessage(_ paho.Client, m paho.Message) {
	msg := message{topic: m.Topic(), payload: append([]byte(nil), m.Payload()...), at: time.Now()}
	s.mu.Lock()
	if len(s.queue) >= queueSize {
		s.queue = s.queue[1:]
		s.dropped++
	}
	s.queue = append(s.queue, msg)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *session) connected() error {
	if s.client == nil || !s.client.IsConnectionOpen() {
		return pluginsdk.Fail("mqtt not connected", errors.New("run mqtt.connect first"))
	}
	return nil
}

type connectConfig struct {
	Broker             string `json:"broker"`
	ClientID           string `json:"clientId"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	CleanSession       *bool  `json:"cleanSession"`
	KeepAlive          string `json:"keepAlive"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	Fresh              bool   `json:"fresh"`
}

func connect(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg connectConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	if cfg.ClientID == "" {
		cfg.ClientID = fmt.Sprintf("stampede-%s-%d", s.run, s.vu)
	}
	key := cfg.Broker + "|" + cfg.ClientID
	if !cfg.Fresh && s.key == key && s.client != nil && s.client.IsConnectionOpen() {
		return &pluginsdk.Result{Skipped: true, Values: map[string]any{"clientId": cfg.ClientID, "reused": true}}, nil
	}
	if s.client != nil {
		s.client.Disconnect(0)
		s.client, s.key, s.subs = nil, "", nil
	}

	keepAlive := 30 * time.Second
	if cfg.KeepAlive != "" {
		d, err := time.ParseDuration(cfg.KeepAlive)
		if err != nil || d <= 0 {
			return nil, pluginsdk.Failf("invalid config", "keepAlive %q is not a duration", cfg.KeepAlive)
		}
		keepAlive = d
	}
	broker := cfg.Broker
	for from, to := range map[string]string{"mqtt://": "tcp://", "mqtts://": "ssl://"} {
		if strings.HasPrefix(broker, from) {
			broker = to + strings.TrimPrefix(broker, from)
		}
	}
	opts := paho.NewClientOptions().AddBroker(broker).SetClientID(cfg.ClientID).
		SetCleanSession(cfg.CleanSession == nil || *cfg.CleanSession).
		SetKeepAlive(keepAlive).SetAutoReconnect(false).SetConnectRetry(false).
		SetOrderMatters(false).SetConnectTimeout(c.Timeout).
		SetDefaultPublishHandler(s.onMessage)
	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}
	if cfg.InsecureSkipVerify {
		opts.SetTLSConfig(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // requested for test brokers
	}
	client := paho.NewClient(opts)
	start := time.Now()
	tok := client.Connect()
	if err := wait(ctx, tok); err != nil {
		client.Disconnect(0)
		return &pluginsdk.Result{Latency: time.Since(start)}, connectError(err)
	}
	lat := time.Since(start)
	s.client, s.key, s.subs = client, key, map[string]bool{}
	present := false
	if ct, ok := tok.(*paho.ConnectToken); ok {
		present = ct.SessionPresent()
	}
	return &pluginsdk.Result{
		Latency: lat, Phases: pluginsdk.Phases{Connect: lat},
		Values: map[string]any{"clientId": cfg.ClientID, "sessionPresent": present, "reused": false},
	}, nil
}

// wait waits for a token until the step's deadline.
func wait(ctx context.Context, tok paho.Token) error {
	select {
	case <-tok.Done():
		return tok.Error()
	case <-ctx.Done():
		return pluginsdk.Fail("mqtt timeout", ctx.Err())
	}
}

func connectError(err error) error {
	var se *pluginsdk.Error
	if errors.As(err, &se) {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "not Authorized"), strings.Contains(msg, "bad user name or password"):
		return pluginsdk.Fail("mqtt not authorized", err)
	case strings.Contains(msg, "identifier rejected"):
		return pluginsdk.Fail("mqtt client id rejected", err)
	case strings.Contains(msg, "refused"), strings.Contains(msg, "server Unavailable"):
		return pluginsdk.Fail("mqtt connection refused", err)
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "i/o timeout"):
		return pluginsdk.Fail("mqtt timeout", err)
	}
	return pluginsdk.Fail("mqtt connect failed", err)
}

type publishConfig struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	QoS     byte   `json:"qos"`
	Retain  bool   `json:"retain"`
}

func publish(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg publishConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	if err := s.connected(); err != nil {
		return nil, err
	}
	start := time.Now()
	s.mark = start
	tok := s.client.Publish(cfg.Topic, cfg.QoS, cfg.Retain, cfg.Payload)
	if err := wait(ctx, tok); err != nil {
		return &pluginsdk.Result{Latency: time.Since(start)}, opError(err)
	}
	res := &pluginsdk.Result{
		Latency: time.Since(start), BytesOut: int64(len(cfg.Payload)),
		Values: map[string]any{"topic": cfg.Topic, "qos": cfg.QoS},
	}
	if pt, ok := tok.(*paho.PublishToken); ok && cfg.QoS > 0 {
		res.Values.(map[string]any)["messageId"] = pt.MessageID()
	}
	return res, nil
}

func opError(err error) error {
	var se *pluginsdk.Error
	if errors.As(err, &se) {
		return err
	}
	if errors.Is(err, paho.ErrNotConnected) {
		return pluginsdk.Fail("mqtt not connected", err)
	}
	return pluginsdk.Fail("mqtt error", err)
}

type subscribeConfig struct {
	Topic string `json:"topic"`
	QoS   byte   `json:"qos"`
}

func subscribe(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg subscribeConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	s := c.Session.(*session)
	if err := s.connected(); err != nil {
		return nil, err
	}
	if s.subs[cfg.Topic] {
		return &pluginsdk.Result{Skipped: true, Values: map[string]any{"topic": cfg.Topic}}, nil
	}
	start := time.Now()
	tok := s.client.Subscribe(cfg.Topic, cfg.QoS, s.onMessage)
	if err := wait(ctx, tok); err != nil {
		return &pluginsdk.Result{Latency: time.Since(start)}, opError(err)
	}
	if st, ok := tok.(*paho.SubscribeToken); ok {
		if code, ok := st.Result()[cfg.Topic]; ok && code == 0x80 {
			return &pluginsdk.Result{Latency: time.Since(start)}, pluginsdk.Failf("mqtt subscribe rejected", "the broker refused %s", cfg.Topic)
		}
	}
	s.subs[cfg.Topic] = true
	s.mark = time.Now()
	return &pluginsdk.Result{Latency: time.Since(start), Values: map[string]any{"topic": cfg.Topic}}, nil
}

type expectConfig struct {
	Topic string         `json:"topic"`
	Match string         `json:"match"`
	JSON  map[string]any `json:"json"`
	Count int            `json:"count"`
}

func expect(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg expectConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	if cfg.Match != "" {
		var err error
		if re, err = regexp.Compile(cfg.Match); err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
	}
	checks, err := jsonChecks(cfg.JSON)
	if err != nil {
		return nil, pluginsdk.Fail("invalid config", err)
	}
	if cfg.Count <= 0 {
		cfg.Count = 1
	}
	s := c.Session.(*session)
	if err := s.connected(); err != nil {
		return nil, err
	}
	mark := s.mark
	if mark.IsZero() {
		mark = time.Now()
	}
	matches := func(m message) bool {
		if cfg.Topic != "" && !topicMatches(cfg.Topic, m.topic) {
			return false
		}
		if re != nil && !re.Match(m.payload) {
			return false
		}
		for _, ch := range checks {
			if !ch(m.payload) {
				return false
			}
		}
		return true
	}

	var got []message
	var bytesIn int64
	for {
		s.mu.Lock()
		rest := s.queue[:0:0]
		for _, m := range s.queue {
			if len(got) < cfg.Count && matches(m) {
				got = append(got, m)
				bytesIn += int64(len(m.payload))
				continue
			}
			rest = append(rest, m)
		}
		s.queue = rest
		s.mu.Unlock()
		if len(got) >= cfg.Count {
			break
		}
		select {
		case <-s.notify:
		case <-ctx.Done():
			return &pluginsdk.Result{Latency: time.Since(mark), BytesIn: bytesIn, Events: int64(len(got))},
				pluginsdk.Failf("mqtt expect timeout", "%d of %d messages arrived", len(got), cfg.Count)
		}
	}

	last := got[len(got)-1]
	res := &pluginsdk.Result{Latency: max(last.at.Sub(mark), 0), BytesIn: bytesIn}
	if cfg.Count > 1 {
		res.Events = int64(len(got))
		res.Phases.FirstEvent = max(got[0].at.Sub(mark), 0)
	}
	vals := map[string]any{"topic": last.topic, "messages": len(got)}
	if utf8.Valid(last.payload) {
		vals["payload"] = string(last.payload)
	}
	var j any
	if json.Unmarshal(last.payload, &j) == nil {
		vals["json"] = j
	}
	res.Values = vals
	return res, nil
}

func disconnect(_ context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	s := c.Session.(*session)
	if s.client == nil {
		return &pluginsdk.Result{Skipped: true}, nil
	}
	start := time.Now()
	s.client.Disconnect(250)
	s.client, s.key, s.subs = nil, "", nil
	return &pluginsdk.Result{Latency: time.Since(start)}, nil
}

// jsonChecks compiles JSONPath ($.a.b, $.list[0]) assertions on a JSON
// payload; "exists" only requires the path.
func jsonChecks(m map[string]any) ([]func([]byte) bool, error) {
	var out []func([]byte) bool
	for path, want := range m {
		if !strings.HasPrefix(path, "$") {
			return nil, fmt.Errorf("json key %q must be a JSONPath starting with $", path)
		}
		g := strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
		g = strings.NewReplacer("[", ".", "]", "").Replace(g)
		if s, ok := want.(string); ok && s == "exists" {
			out = append(out, func(b []byte) bool { return gjson.GetBytes(b, g).Exists() })
			continue
		}
		wantJSON, _ := json.Marshal(want)
		out = append(out, func(b []byte) bool {
			r := gjson.GetBytes(b, g)
			if !r.Exists() {
				return false
			}
			gotJSON, _ := json.Marshal(r.Value())
			return string(gotJSON) == string(wantJSON)
		})
	}
	return out, nil
}

// topicMatches reports whether topic matches an MQTT topic filter.
func topicMatches(filter, topic string) bool {
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, p := range f {
		switch {
		case p == "#":
			return true
		case i >= len(t):
			return false
		case p != "+" && p != t[i]:
			return false
		}
	}
	return len(f) == len(t)
}
