package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/Ivan825/Stampede/internal/metrics"
	"github.com/Ivan825/Stampede/internal/scenario"
)

// testService implements the gRPC interop TestService: UnaryCall echoes
// its payload and reports the caller's metadata; StreamingOutputCall
// sends one message per response parameter.
type testService struct {
	testpb.UnimplementedTestServiceServer
}

func (testService) UnaryCall(ctx context.Context, req *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
	if st := req.GetResponseStatus(); st.GetCode() != 0 {
		return nil, status.Error(codes.Code(st.GetCode()), st.GetMessage()) //nolint:gosec // test codes are small
	}
	md, _ := metadata.FromIncomingContext(ctx)
	traced := "untraced"
	if len(md.Get("traceparent")) > 0 {
		traced = "traced"
	}
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-server", "test-server"))
	return &testpb.SimpleResponse{Payload: req.GetPayload(), Username: strings.Join(md.Get("x-user"), ","), OauthScope: traced}, nil
}

func (testService) StreamingOutputCall(req *testpb.StreamingOutputCallRequest, stream testpb.TestService_StreamingOutputCallServer) error {
	for _, p := range req.GetResponseParameters() {
		select {
		case <-time.After(time.Duration(p.GetIntervalUs()) * time.Microsecond):
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		if err := stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{Body: make([]byte, p.GetSize())}}); err != nil {
			return err
		}
	}
	return nil
}

// grpcServer starts an in-process server with reflection and returns its
// address.
func grpcServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	testpb.RegisterTestServiceServer(s, testService{})
	healthpb.RegisterHealthServer(s, health.NewServer())
	reflection.Register(s)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

const healthProto = `syntax = "proto3";
package grpc.health.v1;
message HealthCheckRequest { string service = 1; }
message HealthCheckResponse {
  enum ServingStatus { UNKNOWN = 0; SERVING = 1; NOT_SERVING = 2; SERVICE_UNKNOWN = 3; }
  ServingStatus status = 1;
}
service Health {
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
  rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
}
`

// descriptorFiles writes the health service as a protoset and as .proto
// source, returning their paths.
func descriptorFiles(t *testing.T) (protoset, protoDir string) {
	t.Helper()
	dir := t.TempDir()
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(healthpb.File_grpc_health_v1_health_proto),
	}}
	b, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	protoset = filepath.Join(dir, "health.protoset")
	if err := os.WriteFile(protoset, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "health.proto"), []byte(healthProto), 0o600); err != nil {
		t.Fatal(err)
	}
	return protoset, dir
}

func TestGRPC(t *testing.T) {
	addr := grpcServer(t)
	protoset, protoDir := descriptorFiles(t)
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := closed.Addr().String()
	closed.Close()

	var mu sync.Mutex
	echoed := map[string]int{}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		echoed[r.URL.Path]++
		mu.Unlock()
	}))
	defer web.Close()

	tests := []struct {
		name    string
		steps   string
		mut     func(*Options)
		wantErr string
		check   func(t *testing.T, out runOut)
	}{
		{
			name: "unary by reflection, metadata in and out, variables flow to HTTP",
			steps: `
      - grpc: grpc.testing.TestService/UnaryCall
        target: grpc://` + addr + `
        message: { payload: { body: "aGVsbG8=" } }
        metadata: { x-user: "user-${vu}" }
        check: { status: OK, json: { "$.payload.body": "aGVsbG8=" } }
        extract: { scope: "$.oauthScope", server: "header:x-server", user: "$.username" }
      - get: /echo/${scope}/${server}/${user}`,
			check: func(t *testing.T, out runOut) {
				st := out.total.Steps[0]
				if st.ChecksPassed != 8 || st.BytesOut == 0 || st.BytesIn == 0 {
					t.Errorf("step stats %+v", st)
				}
				mu.Lock()
				defer mu.Unlock()
				if echoed["/echo/traced/test-server/user-0"]+echoed["/echo/traced/test-server/user-1"] != 4 {
					t.Errorf("extracted values reached HTTP as %v", echoed)
				}
			},
		},
		{
			name:    "non-OK status fails",
			steps:   `[{grpc: grpc.testing.TestService/UnaryCall, target: "grpc://` + addr + `", message: {responseStatus: {code: 5}}}]`,
			wantErr: "gRPC NOT_FOUND",
		},
		{
			name:  "accepted status passes",
			steps: `[{grpc: grpc.testing.TestService/UnaryCall, target: "grpc://` + addr + `", message: {responseStatus: {code: 5}}, check: {status: [OK, NotFound]}}]`,
		},
		{
			name:    "checked status fails as a check",
			steps:   `[{grpc: grpc.testing.TestService/UnaryCall, target: "grpc://` + addr + `", message: {responseStatus: {code: 7}}, check: {status: OK}}]`,
			wantErr: "check status (gRPC PERMISSION_DENIED)",
		},
		{
			name: "server streaming records messages and time to first",
			steps: `
      - grpc: grpc.testing.TestService/StreamingOutputCall
        target: grpc://` + addr + `
        message:
          responseParameters:
            - { size: 4, intervalUs: 20000 }
            - { size: 4, intervalUs: 1000 }
            - { size: 4, intervalUs: 1000 }
        check: { json: { "$.length": 3, "$[2].payload.body": "AAAAAA==" } }`,
			check: func(t *testing.T, out runOut) {
				st := out.total.Steps[0]
				if st.Events != 12 || st.Streams != 4 {
					t.Errorf("events %d streams %d, want 12 and 4", st.Events, st.Streams)
				}
				first := out.res.Phases[0][metrics.PhaseFirstEvent]
				if first.Count() != 4 || first.Quantile(0.5) < 20_000 {
					t.Errorf("first message p50 %dµs over %d calls, want >= 20ms", first.Quantile(0.5), first.Count())
				}
			},
		},
		{
			name:  "descriptors from a protoset",
			steps: `[{grpc: grpc.health.v1.Health/Check, target: "grpc://` + addr + `", protoset: "` + protoset + `", check: {json: {"$.status": SERVING}}}]`,
		},
		{
			name:  "descriptors from .proto source",
			steps: `[{grpc: grpc.health.v1.Health/Check, target: "grpc://` + addr + `", proto: health.proto, importPaths: ["` + protoDir + `"], check: {json: {"$.status": SERVING}}}]`,
		},
		{
			name:    "unknown service",
			steps:   `[{grpc: shop.v1.Nope/Get, target: "grpc://` + addr + `"}]`,
			wantErr: "grpc unknown method",
		},
		{
			name:    "unimplemented method",
			steps:   `[{grpc: grpc.testing.TestService/UnimplementedCall, target: "grpc://` + addr + `"}]`,
			wantErr: "gRPC UNIMPLEMENTED",
		},
		{
			name:    "message that does not fit the input type",
			steps:   `[{grpc: grpc.testing.TestService/UnaryCall, target: "grpc://` + addr + `", message: {nope: 1}}]`,
			wantErr: "grpc invalid message",
		},
		{
			name:    "deadline",
			steps:   `[{grpc: grpc.testing.TestService/StreamingOutputCall, target: "grpc://` + addr + `", timeout: 30ms, message: {responseParameters: [{size: 1, intervalUs: 500000}]}}]`,
			wantErr: "gRPC DEADLINE_EXCEEDED",
		},
		{
			name:    "unreachable target",
			steps:   `[{grpc: grpc.health.v1.Health/Check, target: "grpc://` + deadAddr + `", protoset: "` + protoset + `", timeout: 2s}]`,
			wantErr: "gRPC UNAVAILABLE",
		},
		{
			name:    "safety policy applies",
			steps:   `[{grpc: grpc.health.v1.Health/Check, target: "grpc://` + addr + `"}]`,
			wantErr: "blocked by safety",
			mut:     func(o *Options) { o.AllowHost = func(u *url.URL) bool { return u.Hostname() != "127.0.0.1" } },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, fmt.Sprintf(`
metadata: {name: grpc}
target: {baseURL: %q}
journeys:
  - name: a
    steps: %s
load: {iterations: 4, vus: 2}`, web.URL, tc.steps), tc.mut)
			tot := out.total.Totals()
			if tc.wantErr != "" {
				if tot.Errors[tc.wantErr] != 4 {
					t.Fatalf("errors %v, want 4 x %q", tot.Errors, tc.wantErr)
				}
			} else if tot.Failed != 0 {
				t.Fatalf("errors %v", tot.Errors)
			}
			if tc.check != nil {
				tc.check(t, out)
			}
		})
	}
}

func TestGRPCTargetFromBaseURL(t *testing.T) {
	addr := grpcServer(t)
	out := run(t, fmt.Sprintf(`
metadata: {name: grpc-base}
target: {baseURL: "http://%s"}
journeys: [{name: a, steps: [{grpc: grpc.health.v1.Health/Check}]}]
load: {iterations: 3}`, addr), nil)
	if tot := out.total.Totals(); tot.Requests != 3 || tot.Failed != 0 {
		t.Fatalf("requests %d errors %v", tot.Requests, tot.Errors)
	}
}

func TestGRPCBadDescriptorsFailBeforeTheRun(t *testing.T) {
	_, protoDir := descriptorFiles(t)
	tests := []struct {
		name, step, want string
	}{
		{"missing protoset", `{grpc: a.B/C, protoset: /nonexistent.protoset}`, "no such file"},
		{"missing proto", `{grpc: a.B/C, proto: missing.proto, importPaths: ["` + protoDir + `"]}`, "missing.proto"},
		{"method not in files", `{grpc: grpc.health.v1.Health/Nope, proto: health.proto, importPaths: ["` + protoDir + `"]}`, "has no method Nope"},
		{"client streaming", `{grpc: grpc.testing.TestService/StreamingInputCall, protoset: PROTOSET}`, "streams from the client"},
	}
	// A protoset with the interop TestService, for the client-streaming case.
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(testpb.File_grpc_testing_empty_proto),
		protodesc.ToFileDescriptorProto(testpb.File_grpc_testing_messages_proto),
		protodesc.ToFileDescriptorProto(testpb.File_grpc_testing_test_proto),
	}}
	b, _ := proto.Marshal(set)
	testset := filepath.Join(t.TempDir(), "test.protoset")
	if err := os.WriteFile(testset, b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := scenario.Parse([]byte(fmt.Sprintf(`
metadata: {name: bad}
target: {baseURL: "http://127.0.0.1:1"}
journeys: [{name: a, steps: [%s]}]
load: {iterations: 1}`, strings.ReplaceAll(tc.step, "PROTOSET", testset))))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			prog, err := scenario.Compile(s)
			if err != nil {
				t.Fatal(err)
			}
			plan, _ := s.Load.Plan()
			_, err = New(Options{Program: prog, Plan: plan, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New error %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
