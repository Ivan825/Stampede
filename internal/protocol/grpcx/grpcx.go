// Package grpcx is Stampede's gRPC driver. It calls unary and
// server-streaming methods without generated code: method descriptors
// come from server reflection or from .protoset/.proto files, messages
// are built from JSON with protojson, and responses are rendered back to
// JSON so checks and extractors work as they do for HTTP.
package grpcx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Target is where calls go: a host:port and whether to use TLS.
type Target struct {
	Addr string
	TLS  bool
}

// String renders the target as a URL.
func (t Target) String() string {
	if t.TLS {
		return "grpcs://" + t.Addr
	}
	return "grpc://" + t.Addr
}

// URL is the target as a URL, for host policy checks.
func (t Target) URL() *url.URL {
	u := &url.URL{Scheme: "grpc", Host: t.Addr}
	if t.TLS {
		u.Scheme = "grpcs"
	}
	return u
}

// ParseTarget reads grpc://host:port (plaintext, HTTP/2 prior knowledge)
// or grpcs://host:port (TLS). An http(s) base URL is accepted too, so a
// scenario's target.baseURL can serve gRPC steps. A missing port means
// 443 with TLS and 80 without.
func ParseTarget(s string) (Target, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Target{}, fmt.Errorf("gRPC target %q must look like grpc://host:port or grpcs://host:port", s)
	}
	var t Target
	switch u.Scheme {
	case "grpc", "http":
	case "grpcs", "https":
		t.TLS = true
	default:
		return Target{}, fmt.Errorf("gRPC target %q: scheme must be grpc or grpcs", s)
	}
	if u.Path != "" && u.Path != "/" {
		return Target{}, fmt.Errorf("gRPC target %q must not have a path", s)
	}
	t.Addr = u.Host
	if u.Port() == "" {
		port := "80"
		if t.TLS {
			port = "443"
		}
		t.Addr = net.JoinHostPort(u.Hostname(), port)
	}
	return t, nil
}

// PoolOptions configures client connections.
type PoolOptions struct {
	// Dial replaces the network dialer (DNS cache, tests).
	Dial               func(ctx context.Context, network, addr string) (net.Conn, error)
	InsecureSkipVerify bool
	UserAgent          string
	// PerTarget is how many HTTP/2 connections are opened to each target
	// (default 4). Users are spread over them, so one connection's
	// concurrent-stream limit does not cap the load.
	PerTarget int
}

// Pool holds client connections shared by every virtual user, as a
// service's gRPC client would.
type Pool struct {
	opts  PoolOptions
	mu    sync.Mutex
	conns map[Target][]*grpc.ClientConn
}

// NewPool creates an empty pool; connections open on first use.
func NewPool(o PoolOptions) *Pool {
	if o.PerTarget < 1 {
		o.PerTarget = 4
	}
	return &Pool{opts: o, conns: map[Target][]*grpc.ClientConn{}}
}

// Conn returns the connection user should use for t.
func (p *Pool) Conn(t Target, user int) (*grpc.ClientConn, error) {
	if user < 0 {
		user = -user
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cs := p.conns[t]
	if cs == nil {
		cs = make([]*grpc.ClientConn, p.opts.PerTarget)
		p.conns[t] = cs
	}
	i := user % len(cs)
	if cs[i] == nil {
		c, err := p.dial(t)
		if err != nil {
			return nil, err
		}
		cs[i] = c
	}
	return cs[i], nil
}

func (p *Pool) dial(t Target) (*grpc.ClientConn, error) {
	creds := insecure.NewCredentials()
	if t.TLS {
		creds = credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: p.opts.InsecureSkipVerify, //nolint:gosec // opt-in for test targets
			MinVersion:         tls.VersionTLS12,
		})
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if p.opts.UserAgent != "" {
		opts = append(opts, grpc.WithUserAgent(p.opts.UserAgent))
	}
	if d := p.opts.Dial; d != nil {
		opts = append(opts, grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return d(ctx, "tcp", addr)
		}))
	}
	// passthrough leaves name resolution to the dialer, which may cache it.
	return grpc.NewClient("passthrough:///"+t.Addr, opts...)
}

// Close closes every connection.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cs := range p.conns {
		for _, c := range cs {
			if c != nil {
				_ = c.Close()
			}
		}
	}
	p.conns = map[Target][]*grpc.ClientConn{}
}

// Result is a completed call.
type Result struct {
	Start, End time.Time
	Code       codes.Code
	// Body is the response as protojson; a server stream's messages are
	// a JSON array.
	Body []byte
	// Header and Trailer are the response metadata.
	Header, Trailer metadata.MD
	// Messages counts received messages; First is when the first arrived.
	Messages int
	First    time.Time
	BytesIn  int64
	BytesOut int64
	// Err is the call's error, if any; Code is its status code.
	Err error
}

// ErrInvalidMessage wraps a request message that does not fit the
// method's input type.
var ErrInvalidMessage = errors.New("message does not match the method's input type")

// NewRequest builds the method's input message from JSON.
func NewRequest(m protoreflect.MethodDescriptor, js []byte) (*dynamicpb.Message, error) {
	req := dynamicpb.NewMessage(m.Input())
	if len(js) == 0 {
		return req, nil
	}
	if err := protojson.Unmarshal(js, req); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidMessage, err)
	}
	return req, nil
}

// Invoke calls a unary or server-streaming method. Up to maxBody bytes of
// response JSON are kept.
func Invoke(ctx context.Context, conn *grpc.ClientConn, m protoreflect.MethodDescriptor, req proto.Message, md metadata.MD, maxBody int64) *Result {
	r := &Result{BytesOut: int64(proto.Size(req))}
	ctx = metadata.NewOutgoingContext(ctx, md)
	method := FullMethod(m)
	r.Start = time.Now()
	if !m.IsStreamingServer() {
		resp := dynamicpb.NewMessage(m.Output())
		r.Err = conn.Invoke(ctx, method, req, resp, grpc.Header(&r.Header), grpc.Trailer(&r.Trailer))
		r.End = time.Now()
		r.Code = status.Code(r.Err)
		if r.Err == nil {
			r.Messages, r.First = 1, r.End
			r.BytesIn = int64(proto.Size(resp))
			r.Body, r.Err = protojson.Marshal(resp)
		}
		return r
	}

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, method)
	if err == nil {
		err = stream.SendMsg(req)
	}
	if err == nil {
		err = stream.CloseSend()
	}
	if err != nil {
		r.End, r.Err, r.Code = time.Now(), err, status.Code(err)
		return r
	}
	r.Body = append(r.Body, '[')
	for {
		msg := dynamicpb.NewMessage(m.Output())
		err := stream.RecvMsg(msg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			r.Err = err
			break
		}
		if r.Messages == 0 {
			r.First = time.Now()
		}
		r.Messages++
		r.BytesIn += int64(proto.Size(msg))
		if int64(len(r.Body)) < maxBody {
			b, err := protojson.Marshal(msg)
			if err != nil {
				r.Err = err
				break
			}
			if r.Messages > 1 {
				r.Body = append(r.Body, ',')
			}
			r.Body = append(r.Body, b...)
		}
	}
	r.Body = append(r.Body, ']')
	r.End = time.Now()
	r.Code = status.Code(r.Err)
	r.Header, _ = stream.Header()
	r.Trailer = stream.Trailer()
	return r
}

// FullMethod is the method's path on the wire: /package.Service/Method.
func FullMethod(m protoreflect.MethodDescriptor) string {
	return "/" + string(m.Parent().FullName()) + "/" + string(m.Name())
}

// CodeByName maps a status code name to its code. Names are matched
// without regard to case or underscores, so NOT_FOUND, NotFound and
// not_found all work.
func CodeByName(name string) (codes.Code, bool) {
	key := strings.ReplaceAll(strings.ToUpper(name), "_", "")
	for c := codes.OK; c <= codes.Unauthenticated; c++ {
		if strings.ReplaceAll(strings.ToUpper(CodeName(c)), "_", "") == key {
			return c, true
		}
	}
	if key == "CANCELED" {
		return codes.Canceled, true
	}
	return 0, false
}

// codeNames are the canonical names used in reports.
var codeNames = [...]string{
	"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND",
	"ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION",
	"ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS",
	"UNAUTHENTICATED",
}

// CodeName is a code's canonical name, such as NOT_FOUND.
func CodeName(c codes.Code) string {
	if int(c) < len(codeNames) {
		return codeNames[c]
	}
	return "UNKNOWN"
}
