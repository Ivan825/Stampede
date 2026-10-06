package pki

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"net"
	"testing"
	"time"
)

func seed(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestCAIsDeterministic(t *testing.T) {
	a, err := NewCA(seed(1))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewCA(seed(1))
	c, _ := NewCA(seed(2))
	if !bytes.Equal(a.DER, b.DER) {
		t.Error("the same seed must give a byte-identical CA certificate")
	}
	if bytes.Equal(a.DER, c.DER) || Fingerprint(a.DER) == Fingerprint(c.DER) {
		t.Error("different seeds must give different CAs")
	}
}

func handshake(t *testing.T, server, client *tls.Config) (tls.ConnectionState, tls.ConnectionState, error) {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	srv := tls.Server(a, server)
	cli := tls.Client(b, client)
	errc := make(chan error, 1)
	go func() { errc <- srv.Handshake() }()
	cerr := cli.Handshake()
	serr := <-errc
	if cerr != nil {
		return tls.ConnectionState{}, tls.ConnectionState{}, cerr
	}
	if serr != nil {
		return tls.ConnectionState{}, tls.ConnectionState{}, serr
	}
	return srv.ConnectionState(), cli.ConnectionState(), nil
}

func TestEnrollmentAndMutualTLS(t *testing.T) {
	ca, _ := NewCA(seed(1))
	sc, err := ca.ServerCert([]string{"localhost", "127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := ca.ServerTLS(sc)
	const token = "join-token"

	// Enrollment: no client certificate, proofs bound to the session.
	ss, cs, err := handshake(t, serverCfg, EnrollTLS(Fingerprint(ca.DER)))
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := Binding(ss)
	cb, _ := Binding(cs)
	if !bytes.Equal(sb, cb) {
		t.Fatal("both sides must export the same binding")
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	if !Equal(WorkerProof(token, cb, "w1", pub), WorkerProof(token, sb, "w1", pub)) {
		t.Fatal("worker proof does not verify")
	}
	if Equal(WorkerProof("wrong", cb, "w1", pub), WorkerProof(token, sb, "w1", pub)) {
		t.Fatal("a wrong token must not verify")
	}
	certDER, err := ca.IssueWorker("w1", "abc", pub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(ServerProof(token, sb, certDER, ca.DER), ServerProof(token, cb, certDER, ca.DER)) {
		t.Fatal("server proof does not verify")
	}
	wc, leaf, err := WorkerCert(key, certDER, ca.DER)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "w1" || leaf.URIs[0].String() != WorkerURIPrefix+"abc" {
		t.Errorf("leaf = %v %v", leaf.Subject, leaf.URIs)
	}

	// A second session exports a different binding, so proofs do not replay.
	ss2, _, _ := handshake(t, serverCfg, EnrollTLS(""))
	sb2, _ := Binding(ss2)
	if bytes.Equal(sb, sb2) {
		t.Fatal("bindings must differ between sessions")
	}

	// Mutual TLS: the server sees a verified worker certificate.
	ss, _, err = handshake(t, serverCfg, WorkerTLS(ca.DER, wc))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PeerWorker(ss); err != nil || got.Subject.CommonName != "w1" {
		t.Fatalf("PeerWorker = %v, %v", got, err)
	}
	// Without a certificate the connection succeeds but carries no worker.
	ss, _, err = handshake(t, serverCfg, EnrollTLS(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PeerWorker(ss); err == nil {
		t.Fatal("a connection without a certificate must not count as a worker")
	}
}

func TestWorkerCertificateCannotImpersonateTheServer(t *testing.T) {
	ca, _ := NewCA(seed(1))
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := ca.IssueWorker("evil", "x", pub, time.Hour)
	evil := tls.Certificate{Certificate: [][]byte{der, ca.DER}, PrivateKey: key}
	fake := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{evil}}
	sc, _ := ca.ServerCert(nil, time.Hour)
	if _, _, err := handshake(t, fake, WorkerTLS(ca.DER, sc)); err == nil {
		t.Fatal("a worker certificate must not pass as the server")
	}
}

func TestAnotherCAIsRejected(t *testing.T) {
	ca, _ := NewCA(seed(1))
	other, _ := NewCA(seed(2))
	sc, _ := other.ServerCert(nil, time.Hour)
	if _, _, err := handshake(t, other.ServerTLS(sc), EnrollTLS(Fingerprint(ca.DER))); err == nil {
		t.Fatal("a pinned fingerprint must reject another CA")
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := other.IssueWorker("w", "x", pub, time.Hour)
	if _, _, err := WorkerCert(key, der, ca.DER); err == nil {
		t.Fatal("a certificate from another CA must not be accepted")
	}
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	der2, _ := ca.IssueWorker("w", "x", pub2, time.Hour)
	if _, _, err := WorkerCert(key, der2, ca.DER); err == nil {
		t.Fatal("a certificate for another key must not be accepted")
	}
}
