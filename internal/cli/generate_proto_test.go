package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
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

func TestGenerateFromProto(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	healthpb.RegisterHealthServer(gs, health.NewServer())
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	dir := t.TempDir()
	protos := filepath.Join(dir, "protos")
	if err := os.MkdirAll(protos, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protos, "health.proto"), []byte(healthProtoSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	reply, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "health"},
		"journeys": []any{map[string]any{"name": "probe", "steps": []any{map[string]any{
			"grpc": "grpc.health.v1.Health/Check", "proto": []string{"health.proto"}, "importPaths": []string{protos},
			"message": map[string]any{"service": ""}, "check": map[string]any{"status": "OK", "json": map[string]any{"$.status": "SERVING"}},
		}}}},
		"load": map[string]any{"vus": 1, "duration": "10s"},
	})
	model, calls := fakeModel(t, string(reply))
	out := filepath.Join(dir, "health.yaml")
	text, err := runGen(t, "--proto", filepath.Join(protos, "health.proto"), "--proto-import-path", protos,
		"--target", "http://"+lis.Addr().String(), "--provider", "openai-compatible", "--base-url", model.URL+"/v1", "--model", "local", "-o", out)
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "grpc: grpc.health.v1.Health/Check") || !strings.Contains(string(b), "1 of 1 journeys passed") || calls.Load() != 1 {
		t.Errorf("scenario:\n%s\noutput:\n%s", b, text)
	}
	if _, err := runGen(t, "--proto-import-path", protos, "--describe", "x", "--no-dry-run", "-o", out, "--provider", "openai-compatible", "--base-url", model.URL+"/v1", "--model", "local"); err == nil || !strings.Contains(err.Error(), "--proto-import-path needs --proto") {
		t.Errorf("import path without --proto: %v", err)
	}
}
