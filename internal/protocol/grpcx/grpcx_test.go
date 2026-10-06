package grpcx

import (
	"testing"

	"google.golang.org/grpc/codes"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		in      string
		want    Target
		wantErr bool
	}{
		{"grpc://localhost:9090", Target{Addr: "localhost:9090"}, false},
		{"grpcs://api.example.com", Target{Addr: "api.example.com:443", TLS: true}, false},
		{"grpc://10.0.0.1", Target{Addr: "10.0.0.1:80"}, false},
		{"http://localhost:8080", Target{Addr: "localhost:8080"}, false},
		{"https://shop.test/", Target{Addr: "shop.test:443", TLS: true}, false},
		{"grpc://[::1]:50051", Target{Addr: "[::1]:50051"}, false},
		{"ftp://host:1", Target{}, true},
		{"grpc://host:1/path", Target{}, true},
		{"localhost:9090", Target{}, true},
	}
	for _, tc := range tests {
		got, err := ParseTarget(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseTarget(%q) = %+v, %v; want %+v (error %v)", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestCodeByName(t *testing.T) {
	tests := []struct {
		in   string
		want codes.Code
		ok   bool
	}{
		{"OK", codes.OK, true},
		{"NOT_FOUND", codes.NotFound, true},
		{"NotFound", codes.NotFound, true},
		{"not_found", codes.NotFound, true},
		{"CANCELLED", codes.Canceled, true},
		{"Canceled", codes.Canceled, true},
		{"UNAUTHENTICATED", codes.Unauthenticated, true},
		{"TEAPOT", 0, false},
	}
	for _, tc := range tests {
		got, ok := CodeByName(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("CodeByName(%q) = %v, %v", tc.in, got, ok)
		}
		if ok && CodeName(got) == "" {
			t.Errorf("no name for %v", got)
		}
	}
}
