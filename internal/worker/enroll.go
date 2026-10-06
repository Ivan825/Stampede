package worker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/pki"
)

func rawCerts(cs tls.ConnectionState) [][]byte {
	out := make([][]byte, len(cs.PeerCertificates))
	for i, c := range cs.PeerCertificates {
		out[i] = c.Raw
	}
	return out
}

// identity is a worker's enrolled certificate.
type identity struct {
	key     ed25519.PrivateKey
	cert    tls.Certificate
	caDER   []byte
	issued  time.Time
	expires time.Time
}

// fresh reports whether the certificate is before its half-life.
func (id *identity) fresh(now time.Time) bool {
	return id != nil && now.Before(id.issued.Add(id.expires.Sub(id.issued)/2))
}

// mtlsCreds returns transport credentials for the Connect stream,
// enrolling first when the worker has no certificate or it is past half
// its life. A new key is made for each enrollment; the private key never
// leaves the process.
func (w *Worker) mtlsCreds(ctx context.Context) (credentials.TransportCredentials, error) {
	w.idMu.Lock()
	defer w.idMu.Unlock()
	if !w.ident.fresh(time.Now()) {
		id, err := w.enroll(ctx)
		if err != nil {
			return nil, err
		}
		// Once a CA is known, later enrollments must come from the same CA.
		if w.ident != nil && w.cfg.CAFingerprint == "" {
			w.cfg.CAFingerprint = pki.Fingerprint(w.ident.caDER)
		}
		w.ident = id
	}
	cfg := pki.WorkerTLS(w.ident.caDER, w.ident.cert)
	return credentials.NewTLS(cfg), nil
}

// enroll asks the server for a certificate. It dials TLS itself so it can
// bind its proof to this exact session, then speaks gRPC over that one
// connection.
func (w *Worker) enroll(ctx context.Context) (*identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pin := w.cfg.CAFingerprint
	if pin == "" && w.ident != nil {
		pin = pki.Fingerprint(w.ident.caDER)
	}
	tcfg := pki.EnrollTLS(pin)
	tcfg.NextProtos = []string{"h2"}
	var dialer interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	} = &net.Dialer{}
	if w.cfg.EnrollDialer != nil {
		dialer = w.cfg.EnrollDialer
	}
	raw, err := dialer.DialContext(ctx, "tcp", w.cfg.Server)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, tcfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		if pin != "" {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}
		return nil, err
	}
	state := tc.ConnectionState()
	binding, err := pki.Binding(state)
	if err != nil {
		_ = tc.Close()
		return nil, status.Error(codes.FailedPrecondition, "the server does not offer TLS 1.3; is it started with --worker-mtls?")
	}

	var once sync.Once
	conn, err := grpc.NewClient("passthrough:///"+w.cfg.Server,
		grpc.WithTransportCredentials(insecure.NewCredentials()), // TLS is already up underneath
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			var c net.Conn
			once.Do(func() { c = tc })
			if c == nil {
				return nil, errors.New("the enrollment connection was lost")
			}
			return c, nil
		}))
	if err != nil {
		_ = tc.Close()
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	resp, err := workerv1.NewWorkerServiceClient(conn).Enroll(ctx, &workerv1.EnrollRequest{
		Name: w.cfg.Name, PublicKey: pub, Proof: pki.WorkerProof(w.cfg.Token, binding, w.cfg.Name, pub),
	})
	if err != nil {
		return nil, err
	}
	if !pki.Equal(resp.GetProof(), pki.ServerProof(w.cfg.Token, binding, resp.GetCertificate(), resp.GetCaCertificate())) {
		return nil, status.Error(codes.Unauthenticated, "the server could not prove it knows the join token; refusing its certificate")
	}
	if err := pki.VerifyServer(resp.GetCaCertificate())(rawCerts(state), nil); err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	if pin != "" && pki.Fingerprint(resp.GetCaCertificate()) != pin {
		return nil, status.Error(codes.Unauthenticated, fmt.Sprintf("the server's CA changed (expected %s)", pin))
	}
	cert, leaf, err := pki.WorkerCert(key, resp.GetCertificate(), resp.GetCaCertificate())
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	w.log.Info("enrolled with the server's CA", "ca", pki.Fingerprint(resp.GetCaCertificate()), "certificate", resp.GetWorkerId(), "expires", leaf.NotAfter.Format(time.RFC3339))
	return &identity{key: key, cert: cert, caDER: resp.GetCaCertificate(), issued: time.Now(), expires: leaf.NotAfter}, nil
}
