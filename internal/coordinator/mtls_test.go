package coordinator_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Ivan825/Stampede/internal/coordinator"
	"github.com/Ivan825/Stampede/internal/pki"
	"github.com/Ivan825/Stampede/internal/worker"
)

func mtlsServer(t *testing.T, token string) (*coordinator.Coordinator, *pki.CA, string) {
	t.Helper()
	ca, err := pki.NewCA(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ca.ServerCert([]string{"127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := coordinator.New(coordinator.Config{JoinToken: token, CA: ca, Logger: log})
	gs := grpc.NewServer(coordinator.ServerOptions(ca.ServerTLS(sc))...)
	c.Register(gs)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(ln) }()
	t.Cleanup(gs.Stop)
	return c, ca, ln.Addr().String()
}

func runWorker(t *testing.T, cfg worker.Config) (context.CancelFunc, chan error) {
	t.Helper()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.New(cfg).Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel, done
}

func waitWorkers(t *testing.T, c *coordinator.Coordinator, n int) []coordinator.WorkerInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ws := c.Workers(); len(ws) >= n {
			return ws
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected %d workers, have %d", n, len(c.Workers()))
	return nil
}

func TestWorkerEnrollsAndConnectsWithMutualTLS(t *testing.T) {
	c, ca, addr := mtlsServer(t, "s3cret")
	runWorker(t, worker.Config{Server: addr, Token: "s3cret", Name: "w-pinned", MTLS: true, CAFingerprint: pki.Fingerprint(ca.DER)})
	runWorker(t, worker.Config{Server: addr, Token: "s3cret", Name: "w-tofu", MTLS: true})
	ws := waitWorkers(t, c, 2)
	names := map[string]bool{}
	for _, w := range ws {
		names[w.Name] = true
	}
	if !names["w-pinned"] || !names["w-tofu"] {
		t.Errorf("workers = %+v", ws)
	}
}

func TestEnrollmentWithAWrongTokenFails(t *testing.T) {
	c, _, addr := mtlsServer(t, "s3cret")
	_, done := runWorker(t, worker.Config{Server: addr, Token: "wrong", Name: "w", MTLS: true})
	select {
	case err := <-done:
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v, want Unauthenticated", err)
		}
		done <- err // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("a worker with a wrong token must stop")
	}
	if len(c.Workers()) != 0 {
		t.Error("no worker may connect")
	}
}

func TestPinnedFingerprintRejectsAnotherCA(t *testing.T) {
	_, _, addr := mtlsServer(t, "s3cret")
	other, _ := pki.NewCA(bytes.Repeat([]byte{8}, 32))
	_, done := runWorker(t, worker.Config{Server: addr, Token: "s3cret", Name: "w", MTLS: true, CAFingerprint: pki.Fingerprint(other.DER)})
	select {
	case err := <-done:
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v, want Unauthenticated", err)
		}
		done <- err
	case <-time.After(10 * time.Second):
		t.Fatal("a pinned worker must refuse another CA")
	}
}

func TestConnectWithoutCertificateIsRefused(t *testing.T) {
	c, _, addr := mtlsServer(t, "s3cret")
	// The old way: TLS without a client certificate, token in Hello.
	_, done := runWorker(t, worker.Config{
		Server: addr, Token: "s3cret", Name: "w",
		TLS: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true}, //nolint:gosec // test
	})
	select {
	case err := <-done:
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("err = %v, want Unauthenticated", err)
		}
		done <- err
	case <-time.After(10 * time.Second):
		t.Fatal("a worker without a certificate must be refused")
	}
	if len(c.Workers()) != 0 {
		t.Error("no worker may connect")
	}
}
