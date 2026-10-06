package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Ivan825/Stampede/pkg/pluginsdk"
)

const version = "0.1.0"

const commandSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["addr"],
  "oneOf": [{"required": ["command"]}, {"required": ["pipeline"]}],
  "properties": {
    "addr": {"type": "string", "x-stampede-target": true, "description": "host:port, or a redis:// or rediss:// URL (which may carry the password and database)."},
    "command": {"$ref": "#/$defs/command"},
    "pipeline": {"type": "array", "minItems": 1, "items": {"$ref": "#/$defs/command"}, "description": "Commands sent together in one round trip."},
    "username": {"type": "string"},
    "password": {"type": "string"},
    "db": {"type": "integer", "minimum": 0},
    "tls": {"type": "boolean", "description": "Connect with TLS (rediss:// URLs always do)."},
    "insecureSkipVerify": {"type": "boolean", "description": "Skip TLS certificate checks (test servers only)."}
  },
  "$defs": {
    "command": {
      "description": "A command and its arguments, such as [SET, key, value] or \"GET key\".",
      "oneOf": [
        {"type": "string", "minLength": 1},
        {"type": "array", "minItems": 1, "items": {"type": ["string", "number", "boolean"]}}
      ]
    }
  }
}`

func newPlugin() *pluginsdk.Plugin {
	return &pluginsdk.Plugin{
		Name:        "redis",
		Version:     version,
		Description: "Runs Redis commands and pipelines on each user's own connection.",
		NewSession: func(context.Context, pluginsdk.SessionInfo) (any, error) {
			return &session{clients: map[string]*redis.Client{}}, nil
		},
		Steps: []pluginsdk.Step{{
			Name:        "command",
			Description: "Run one command, or a pipeline of commands in one round trip.",
			Schema:      commandSchema,
			Run:         command,
		}},
	}
}

// session holds one client (a single connection) per server setup.
type session struct {
	clients map[string]*redis.Client
}

func (s *session) Close() error {
	var errs []error
	for _, c := range s.clients {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

type commandConfig struct {
	Addr               string `json:"addr"`
	Command            any    `json:"command"`
	Pipeline           []any  `json:"pipeline"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	DB                 int    `json:"db"`
	TLS                bool   `json:"tls"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
}

func (s *session) client(cfg *commandConfig) (*redis.Client, error) {
	key := fmt.Sprintf("%s|%s|%s|%d|%t|%t", cfg.Addr, cfg.Username, cfg.Password, cfg.DB, cfg.TLS, cfg.InsecureSkipVerify)
	if c := s.clients[key]; c != nil {
		return c, nil
	}
	var opts *redis.Options
	if strings.Contains(cfg.Addr, "://") {
		o, err := redis.ParseURL(cfg.Addr)
		if err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
		opts = o
	} else {
		opts = &redis.Options{Addr: cfg.Addr}
	}
	if cfg.Username != "" {
		opts.Username = cfg.Username
	}
	if cfg.Password != "" {
		opts.Password = cfg.Password
	}
	if cfg.DB != 0 {
		opts.DB = cfg.DB
	}
	if cfg.TLS && opts.TLSConfig == nil {
		host, _, _ := net.SplitHostPort(opts.Addr)
		opts.TLSConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	if opts.TLSConfig != nil && cfg.InsecureSkipVerify {
		opts.TLSConfig.InsecureSkipVerify = true //nolint:gosec // requested for test servers
	}
	// One connection per user, as for a real client process; the step's
	// timeout bounds every call.
	opts.PoolSize, opts.MinIdleConns, opts.MaxRetries = 1, 0, -1
	opts.Protocol = 2
	opts.DisableIdentity = true
	c := redis.NewClient(opts)
	s.clients[key] = c
	return c, nil
}

// args turns a command given as a string or a list into arguments.
func args(cmd any) ([]any, error) {
	switch x := cmd.(type) {
	case string:
		f := strings.Fields(x)
		if len(f) == 0 {
			return nil, errors.New("empty command")
		}
		out := make([]any, len(f))
		for i, a := range f {
			out[i] = a
		}
		return out, nil
	case []any:
		if len(x) == 0 {
			return nil, errors.New("empty command")
		}
		out := make([]any, len(x))
		for i, a := range x {
			if f, ok := a.(float64); ok && f == float64(int64(f)) {
				out[i] = int64(f)
			} else {
				out[i] = a
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("a command is a string or a list, not %T", cmd)
}

func command(ctx context.Context, c *pluginsdk.Call) (*pluginsdk.Result, error) {
	var cfg commandConfig
	if err := c.Decode(&cfg); err != nil {
		return nil, err
	}
	cmds := cfg.Pipeline
	if cfg.Command != nil {
		cmds = []any{cfg.Command}
	}
	argv := make([][]any, len(cmds))
	var out int64
	for i, cmd := range cmds {
		a, err := args(cmd)
		if err != nil {
			return nil, pluginsdk.Fail("invalid config", err)
		}
		argv[i] = a
		out += respLen(a)
	}
	client, err := c.Session.(*session).client(&cfg)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	var results []*redis.Cmd
	if cfg.Command != nil {
		results = []*redis.Cmd{client.Do(ctx, argv[0]...)}
	} else {
		pipe := client.Pipeline()
		for _, a := range argv {
			results = append(results, pipe.Do(ctx, a...))
		}
		_, _ = pipe.Exec(ctx)
	}
	res := &pluginsdk.Result{Latency: time.Since(start), BytesOut: out}

	values := make([]any, len(results))
	var firstErr error
	for i, r := range results {
		v, err := r.Result()
		switch {
		case errors.Is(err, redis.Nil):
			values[i] = nil
		case err != nil:
			if firstErr == nil {
				firstErr = err
			}
			values[i] = map[string]any{"error": err.Error()}
		default:
			values[i] = jsonable(v)
		}
		res.BytesIn += replyLen(values[i])
	}
	if cfg.Command != nil {
		res.Values = map[string]any{"value": values[0]}
	} else {
		res.Values = map[string]any{"values": values}
	}
	if firstErr != nil {
		return res, classify(firstErr)
	}
	return res, nil
}

// jsonable converts a reply into JSON-friendly values.
func jsonable(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonable(e)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[fmt.Sprint(k)] = jsonable(e)
		}
		return out
	case []byte:
		return string(x)
	}
	return v
}

// respLen is the size of a command in the Redis protocol.
func respLen(a []any) int64 {
	n := int64(len(fmt.Sprintf("*%d\r\n", len(a))))
	for _, x := range a {
		s := fmt.Sprint(x)
		n += int64(len(fmt.Sprintf("$%d\r\n", len(s))) + len(s) + 2)
	}
	return n
}

// replyLen approximates a reply's size: its payload bytes.
func replyLen(v any) int64 {
	switch x := v.(type) {
	case string:
		return int64(len(x))
	case []any:
		var n int64
		for _, e := range x {
			n += replyLen(e)
		}
		return n
	case map[string]any:
		var n int64
		for k, e := range x {
			n += int64(len(k)) + replyLen(e)
		}
		return n
	case nil:
		return 0
	}
	return int64(len(fmt.Sprint(v)))
}

var codeRe = regexp.MustCompile(`^[A-Z][A-Z_]{1,23}$`)

func classify(err error) error {
	var ne net.Error
	var re redis.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return pluginsdk.Fail("redis timeout", err)
	case errors.As(err, &re):
		// Error replies start with a code: ERR, WRONGTYPE, NOAUTH, MOVED...
		code, _, _ := strings.Cut(re.Error(), " ")
		if codeRe.MatchString(code) {
			return pluginsdk.Fail("redis "+code, err)
		}
		return pluginsdk.Fail("redis error", err)
	case errors.As(err, &ne), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, net.ErrClosed), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		// A pooled connection to a server that went away fails with EOF or
		// a reset rather than a dial error.
		return pluginsdk.Fail("redis connection error", err)
	}
	return pluginsdk.Fail("redis error", err)
}
