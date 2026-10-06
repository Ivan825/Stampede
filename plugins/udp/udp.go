package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

// version is the plugin's version.
const version = "0.1.0"

const sendSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["addr"],
  "properties": {
    "addr": {"type": "string", "x-stampede-target": true, "description": "host:port to send to."},
    "payload": {"type": "string", "description": "The datagram, as text or in the given encoding."},
    "encoding": {"enum": ["text", "hex", "base64"], "description": "How payload is written (default text)."},
    "reply": {"type": "boolean", "description": "Wait for a reply; latency is then the round trip (default false)."},
    "match": {"type": "string", "description": "Regex a reply must match; other datagrams are skipped (stale replies)."},
    "maxReply": {"type": "integer", "minimum": 1, "maximum": 65535, "description": "Largest reply read, in bytes (default 65535)."}
  }
}`

func newPlugin() *pluginsdk.Plugin {
	return &pluginsdk.Plugin{
		Name:        "udp",
		Version:     version,
		Description: "Sends UDP datagrams and times replies.",
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{conns: map[string]net.Conn{}}, nil
		},
		Steps: []pluginsdk.Step{{
			Name:        "send",
			Description: "Send a datagram from the user's own socket; with reply: true, wait for the answer.",
			Schema:      sendSchema,
			Run:         send,
		}},
	}
}

// session keeps one connected socket per address, so a user keeps its
// source port like a real client.
type session struct {
	conns map[string]net.Conn
}

func (s *session) Close() error {
	var errs []error
	for _, c := range s.conns {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

func (s *session) conn(ctx context.Context, addr string) (net.Conn, error) {
	if c := s.conns[addr]; c != nil {
		return c, nil
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, err
	}
	s.conns[addr] = c
	return c, nil
}

func (s *session) drop(addr string) {
	if c := s.conns[addr]; c != nil {
		_ = c.Close()
		delete(s.conns, addr)
	}
}

type sendConfig struct {
	Addr     string `json:"addr"`
	Payload  string `json:"payload"`
	Encoding string `json:"encoding"`
	Reply    bool   `json:"reply"`
	Match    string `json:"match"`
	MaxReply int    `json:"maxReply"`
}

func decodePayload(p, enc string) ([]byte, error) {
	switch enc {
	case "", "text":
		return []byte(p), nil
	case "hex":
		return hex.DecodeString(strings.Join(strings.Fields(p), ""))
	case "base64":
		return base64.StdEncoding.DecodeString(p)
	}
	return nil, errors.New("unknown encoding " + enc)
}

func send(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg sendConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	payload, err := decodePayload(cfg.Payload, cfg.Encoding)
	if err != nil {
		return nil, pluginsdk.Fail("invalid config", err)
	}
	var match *regexp.Regexp
	if cfg.Match != "" {
		if match, err = regexp.Compile(cfg.Match); err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
	}
	if cfg.MaxReply <= 0 {
		cfg.MaxReply = 65535
	}
	s := c.Session.(*session)
	conn, err := s.conn(ctx, cfg.Addr)
	if err != nil {
		return nil, pluginsdk.Fail("udp error", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	start := time.Now()
	n, err := conn.Write(payload)
	if err != nil {
		s.drop(cfg.Addr)
		return nil, classify(err)
	}
	res := &pluginsdk.Result{BytesOut: int64(n), Values: map[string]any{"bytesSent": n}}
	if !cfg.Reply {
		res.Latency = time.Since(start)
		return res, nil
	}

	buf := make([]byte, cfg.MaxReply)
	for {
		m, err := conn.Read(buf)
		if err != nil {
			res.Latency = time.Since(start)
			if isRefused(err) {
				s.drop(cfg.Addr)
			}
			return res, classify(err)
		}
		res.BytesIn += int64(m)
		reply := buf[:m]
		if match != nil && !match.Match(reply) {
			continue
		}
		res.Latency = time.Since(start)
		res.Phases.Wait = res.Latency
		vals := res.Values.(map[string]any)
		vals["replyBytes"] = m
		vals["replyHex"] = hex.EncodeToString(reply)
		if utf8.Valid(reply) {
			vals["reply"] = string(reply)
		} else {
			vals["reply"] = base64.StdEncoding.EncodeToString(reply)
			vals["replyEncoding"] = "base64"
		}
		return res, nil
	}
}

func isRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

func classify(err error) error {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return pluginsdk.Fail("udp timeout", err)
	case isRefused(err):
		return pluginsdk.Fail("udp refused", err)
	}
	return pluginsdk.Fail("udp error", err)
}
