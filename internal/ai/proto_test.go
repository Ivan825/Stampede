package ai

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/Ivan825/Stampede/internal/ai/provider"
)

const healthProto = `syntax = "proto3";
package grpc.health.v1;
message HealthCheckRequest { string service = 1; }
message HealthCheckResponse {
  enum ServingStatus { UNKNOWN = 0; SERVING = 1; NOT_SERVING = 2; SERVICE_UNKNOWN = 3; }
  ServingStatus status = 1;
}
// Health reports whether services are serving.
service Health {
  // Check returns a service's status.
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
  rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
  rpc Chat(stream HealthCheckRequest) returns (stream HealthCheckResponse);
}
`

// healthServer serves grpc.health.v1.Health without reflection, so the
// dry run can only find the method in the .proto input.
func healthServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	healthpb.RegisterHealthServer(s, health.NewServer())
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	return lis.Addr().String()
}

func TestUnderstandProto(t *testing.T) {
	und, err := Understand(Inputs{Proto: map[string][]byte{"health.proto": []byte(healthProto)}, ProtoPaths: []string{"protos/health.proto"}}, NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if len(und.GRPCMethods) != 2 {
		t.Fatalf("methods: %+v", und.GRPCMethods)
	}
	check := und.GRPCMethods[0]
	if check.Name != "grpc.health.v1.Health/Check" || check.ServerStreaming || check.Example != `{"service":"string"}` || check.Comment != "Check returns a service's status." {
		t.Errorf("Check: %+v", check)
	}
	if !und.GRPCMethods[1].ServerStreaming {
		t.Errorf("Watch streams from the server: %+v", und.GRPCMethods[1])
	}
	for _, want := range []string{
		"## gRPC services", "- grpc.health.v1.Health/Check (unary): grpc.health.v1.HealthCheckRequest → grpc.health.v1.HealthCheckResponse",
		"proto: [protos/health.proto]", "Not callable from a scenario (client or bidirectional streaming): grpc.health.v1.Health/Chat",
	} {
		if !strings.Contains(und.Context, want) {
			t.Errorf("context lacks %q:\n%s", want, und.Context)
		}
	}
	if len(und.Endpoints) != 2 || und.Endpoints[0].Key() != "GRPC grpc.health.v1.Health/Check" || und.HasSpec {
		t.Errorf("endpoints: %+v (hasSpec %v)", und.Endpoints, und.HasSpec)
	}
	if _, err := Understand(Inputs{Proto: map[string][]byte{"bad.proto": []byte("syntax = \"proto3\"; message {")}}, NewRedactor()); err == nil || !strings.Contains(err.Error(), "proto:") {
		t.Errorf("bad proto: %v", err)
	}
}

// TestGenerateFromProto drafts grpc steps from a .proto file, dry-runs them
// against a server without reflection (descriptors come from the input),
// and repairs a call that fails.
func TestGenerateFromProto(t *testing.T) {
	addr := healthServer(t)
	draft1 := `{"metadata":{"name":"health"},"journeys":[{"name":"probe","steps":[
	  {"name":"check","grpc":"grpc.health.v1.Health/Check","message":{"service":"payments"},"check":{"status":"OK"}},
	  {"name":"other","grpc":"grpc.health.v1.Health/Ping"}]}],"load":{"vus":1,"duration":"10s"}}`
	draft2 := `{"metadata":{"name":"health"},"journeys":[{"name":"probe","steps":[
	  {"name":"check","grpc":"grpc.health.v1.Health/Check","message":{"service":""},"check":{"status":"OK","json":{"$.status":"SERVING"}},"extract":{"state":"$.status"}}]}],
	  "load":{"vus":1,"duration":"10s"}}`
	fake := &provider.Fake{Replies: []any{draft1, draft2}}
	res, err := Generate(context.Background(), Inputs{Proto: map[string][]byte{"health.proto": []byte(healthProto)}},
		Options{Provider: fake, Target: "http://" + addr, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Validated() || res.Rounds != 1 {
		t.Fatalf("result: validated %v, rounds %d, problems %v, journeys %+v", res.Validated(), res.Rounds, res.Problems, res.Journeys)
	}
	reqs := fake.Requests()
	if !strings.Contains(reqs[0].Messages[0].Content, "grpc.health.v1.Health/Check") || !strings.Contains(reqs[0].Messages[0].Content, "Leave `proto:` out") {
		t.Errorf("draft prompt: %s", reqs[0].Messages[0].Content)
	}
	repair := reqs[1].Messages[2].Content
	if !strings.Contains(repair, "grpc grpc.health.v1.Health/Ping is not a method of the .proto files") || !strings.Contains(repair, "NOT_FOUND") {
		t.Errorf("repair prompt lacks the problems:\n%s", repair)
	}
	tr := res.Journeys[0].Traces[0].Steps[0]
	if !tr.OK || tr.Method != "GRPC" || tr.Status != 0 || !strings.Contains(tr.ResponseBody, "SERVING") || tr.Extracted["state"] != "SERVING" {
		t.Errorf("trace: %+v", tr)
	}
	if !strings.Contains(res.YAML, "grpc: grpc.health.v1.Health/Check") {
		t.Errorf("yaml:\n%s", res.YAML)
	}
}
