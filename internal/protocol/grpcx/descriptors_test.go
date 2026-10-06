package grpcx

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
	reflectv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

// reflectionServer serves the health and interop test services with the
// given reflection registration.
func reflectionServer(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	healthpb.RegisterHealthServer(s, health.NewServer())
	testpb.RegisterTestServiceServer(s, testpb.UnimplementedTestServiceServer{})
	register(s)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestReflect(t *testing.T) {
	tests := []struct {
		name     string
		register func(*grpc.Server)
	}{
		{"v1", func(s *grpc.Server) { reflection.RegisterV1(s) }},
		{"v1alpha only", func(s *grpc.Server) {
			reflectv1alpha.RegisterServerReflectionServer(s, reflection.NewServer(reflection.ServerOptions{Services: s}))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := reflectionServer(t, tc.register)
			d := NewDescriptors()
			target := Target{Addr: "test"}
			// The test service imports other files, which must be fetched
			// and linked too.
			m, err := d.Method(context.Background(), conn, target, Source{}, "grpc.testing.TestService", "StreamingOutputCall")
			if err != nil {
				t.Fatal(err)
			}
			if !m.IsStreamingServer() || m.Input().FullName() != "grpc.testing.StreamingOutputCallRequest" {
				t.Errorf("method %v", m.FullName())
			}
			if FullMethod(m) != "/grpc.testing.TestService/StreamingOutputCall" {
				t.Errorf("full method %q", FullMethod(m))
			}
			if _, err := d.Method(context.Background(), conn, target, Source{}, "grpc.testing.TestService", "FullDuplexCall"); !errors.Is(err, ErrUnknownMethod) {
				t.Errorf("bidirectional streaming must be refused, got %v", err)
			}
			if _, err := d.Method(context.Background(), conn, target, Source{}, "shop.Nope", "Get"); !errors.Is(err, ErrUnknownMethod) {
				t.Errorf("unknown service: got %v", err)
			}
		})
	}
}

func TestNewRequest(t *testing.T) {
	m := healthpb.File_grpc_health_v1_health_proto.Services().ByName("Health").Methods().ByName("Check")
	if _, err := NewRequest(m, []byte(`{"service":"shop"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRequest(m, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRequest(m, []byte(`{"nope":1}`)); !errors.Is(err, ErrInvalidMessage) {
		t.Errorf("unknown field: got %v", err)
	}
}
