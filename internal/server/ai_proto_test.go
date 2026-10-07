package server_test

import (
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/Ivan825/Stampede/internal/ai/provider"
)

const healthProtoSrc = `syntax = "proto3";
package grpc.health.v1;
message HealthCheckRequest { string service = 1; }
message HealthCheckResponse {
  enum ServingStatus { UNKNOWN = 0; SERVING = 1; NOT_SERVING = 2; SERVICE_UNKNOWN = 3; }
  ServingStatus status = 1;
}
service Health {
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);
}
`

// TestAIJobFromProto generates grpc steps from .proto sources sent to the
// API and dry-runs them against a gRPC target without reflection.
func TestAIJobFromProto(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	healthpb.RegisterHealthServer(gs, health.NewServer())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	fakes := &aiFakes{}
	base, _, _ := startAIServer(t, fakes)
	c := newClient(t, base)
	setup(t, c)
	var proj, tgt map[string]any
	c.do("POST", "/projects", map[string]string{"name": "grpc"}, &proj)
	pid := proj["id"].(string)
	if code := c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "health", "baseURL": "http://" + lis.Addr().String()}, &tgt); code != 201 {
		t.Fatalf("target: %d", code)
	}
	fake := &provider.Fake{Replies: []any{`{"metadata":{"name":"health"},"journeys":[{"name":"probe","steps":[
	  {"grpc":"grpc.health.v1.Health/Check","message":{"service":""},"check":{"status":"OK","json":{"$.status":"SERVING"}}}]}],
	  "load":{"vus":1,"duration":"10s"}}`}}
	fakes.set(fake)
	c.do("POST", "/ai/providers", map[string]any{"kind": "anthropic", "apiKey": "sk-test", "model": "m"}, nil)

	var e errBody
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", map[string]any{"proto": map[string]string{"../x.proto": healthProtoSrc}, "targetId": tgt["id"]}, &e); code != 422 {
		t.Errorf("proto name outside the tree: %d %+v", code, e)
	}
	var job map[string]any
	if code := c.do("POST", "/projects/"+pid+"/ai/jobs", map[string]any{"proto": map[string]string{"grpc/health/v1/health.proto": healthProtoSrc}, "targetId": tgt["id"]}, &job); code != 202 {
		t.Fatalf("job: %d %v", code, job)
	}
	done := waitJob(t, c, job["id"].(string))
	if done.Status != "succeeded" || !strings.Contains(done.Yaml, "grpc: grpc.health.v1.Health/Check") {
		t.Fatalf("job: %s %q\n%s", done.Status, done.Error, done.Yaml)
	}
	if len(done.Journeys) != 1 || done.Journeys[0].Status != "passed" || !strings.Contains(done.Journeys[0].Traces[0].Steps[0].ResponseBody, "SERVING") {
		t.Errorf("journeys: %+v", done.Journeys)
	}
	if p := fake.Requests()[0].Messages[0].Content; !strings.Contains(p, "grpc.health.v1.Health/Check (unary)") || !strings.Contains(p, "Leave `proto:` out") {
		t.Errorf("prompt: %s", p)
	}
}
