package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerFlags(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		f    workerFlags
		env  string
		err  string
	}{
		{name: "minimal", f: workerFlags{server: "s:1", token: "t"}},
		{name: "token from env", f: workerFlags{server: "s:1"}, env: "from-env"},
		{name: "no server", f: workerFlags{token: "t"}, err: "--server is required"},
		{name: "no token", f: workerFlags{server: "s:1"}, err: "join token is required"},
		{name: "labels", f: workerFlags{server: "s:1", token: "t", labels: []string{"pool=spot", "zone=a=b"}}},
		{name: "bad label", f: workerFlags{server: "s:1", token: "t", labels: []string{"pool"}}, err: "use KEY=VALUE"},
		{name: "negative vus", f: workerFlags{server: "s:1", token: "t", maxVUs: -1}, err: "must not be negative"},
		{name: "ca and insecure", f: workerFlags{server: "s:1", token: "t", caFile: notPEM, insecure: true}, err: "contradict"},
		{name: "ca not pem", f: workerFlags{server: "s:1", token: "t", caFile: notPEM}, err: "no PEM certificates"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("STAMPEDE_JOIN_TOKEN", tt.env)
			cfg, err := tt.f.config()
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.env != "" && cfg.Token != tt.env {
				t.Errorf("token %q", cfg.Token)
			}
			if len(tt.f.labels) > 0 && (cfg.Labels["pool"] != "spot" || cfg.Labels["zone"] != "a=b") {
				t.Errorf("labels %v", cfg.Labels)
			}
		})
	}
}
