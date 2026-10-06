// Package pki is the server's built-in certificate authority for workers.
//
// The CA key is derived from the server's master key, so every replica of
// a server has the same CA without storing or sharing anything, and the
// CA certificate is byte-identical everywhere (Ed25519 signatures are
// deterministic and every field is fixed).
//
// A worker enrolls by proving it knows the join token without sending it:
// both sides compute an HMAC keyed by the token over keying material
// exported from their TLS session, so a proof made for one connection is
// useless on another and a machine in the middle, holding two different
// sessions, can neither pass nor relay it. The server answers with a
// certificate for the worker's own public key and the CA certificate,
// both covered by the server's HMAC, which is how the worker learns to
// trust the CA. From then on each side verifies the other's certificate:
// the server presents a certificate valid only for server authentication,
// workers present certificates valid only for client authentication, so a
// worker's certificate can never impersonate the server.
package pki

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"time"
)

// Purpose is the keyring derivation label for the CA key. Changing it
// changes the CA, invalidating every issued certificate.
const Purpose = "stampede worker CA v1"

// ExporterLabel is the TLS exporter label enrollment proofs are bound to.
const ExporterLabel = "EXPORTER-stampede-enroll"

// WorkerURIPrefix starts the URI SAN that names a worker in its certificate.
const WorkerURIPrefix = "stampede://worker/"

// CA is a certificate authority with an Ed25519 key.
type CA struct {
	key  ed25519.PrivateKey
	Cert *x509.Certificate
	DER  []byte
}

// epoch is the fixed NotBefore of the CA certificate.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// NewCA builds the CA from a 32-byte seed (from keyring.Derive(Purpose, 32)).
func NewCA(seed []byte) (*CA, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("CA seed must be %d bytes", ed25519.SeedSize)
	}
	key := ed25519.NewKeyFromSeed(seed)
	pub := key.Public().(ed25519.PublicKey)
	skid := sha256.Sum256(pub)
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(skid[:16]),
		Subject:               pkix.Name{CommonName: "Stampede worker CA " + hex.EncodeToString(skid[:4]), Organization: []string{"Stampede"}},
		NotBefore:             epoch,
		NotAfter:              epoch.AddDate(100, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          skid[:20],
	}
	der, err := x509.CreateCertificate(nil, tmpl, tmpl, pub, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{key: key, Cert: cert, DER: der}, nil
}

// Fingerprint is "sha256:" and the hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Pool is a pool holding only the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

func (ca *CA) issue(tmpl *x509.Certificate, pub crypto.PublicKey) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	tmpl.SerialNumber = serial
	tmpl.AuthorityKeyId = ca.Cert.SubjectKeyId
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.BasicConstraintsValid = true
	return x509.CreateCertificate(nil, tmpl, ca.Cert, pub, ca.key)
}

// ServerCert issues a certificate for the worker port, valid for server
// authentication only. Workers check it against the CA and the usage, not
// against a host name, so hosts are informational.
func (ca *CA) ServerCert(hosts []string, validity time.Duration) (tls.Certificate, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "stampede server", Organization: []string{"Stampede"}},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(validity),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := ca.issue(tmpl, pub)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.DER}, PrivateKey: key}, nil
}

// IssueWorker issues a client certificate for a worker's Ed25519 public key.
func (ca *CA) IssueWorker(name, id string, pub ed25519.PublicKey, validity time.Duration) ([]byte, error) {
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("the worker public key must be a 32-byte Ed25519 key")
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: name, Organization: []string{"Stampede worker"}},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(validity),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if u, err := parseURI(WorkerURIPrefix + id); err == nil {
		tmpl.URIs = append(tmpl.URIs, u)
	}
	return ca.issue(tmpl, pub)
}

// ServerTLS is the worker port's TLS configuration: TLS 1.3 (enrollment
// proofs need exported keying material), the CA-issued server certificate,
// and client certificates verified when given. The coordinator decides
// whether a connection needs one (the Connect stream does, Enroll does not).
func (ca *CA) ServerTLS(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    ca.Pool(),
	}
}

// VerifyServer checks a server's certificate chain against caDER and
// requires server-authentication usage. It does not check a host name:
// the CA is private to one server and issues no other server certificates.
func VerifyServer(caDER []byte) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		ca, err := x509.ParseCertificate(caDER)
		if err != nil {
			return err
		}
		if len(rawCerts) == 0 {
			return errors.New("the server sent no certificate")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		roots.AddCert(ca)
		_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if err != nil {
			return fmt.Errorf("the server certificate is not from this server's CA: %w", err)
		}
		return nil
	}
}

// WorkerTLS is a worker's TLS configuration once it holds a certificate.
func WorkerTLS(caDER []byte, cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		// The chain is checked by VerifyPeerCertificate against the pinned
		// CA instead of the system roots and a host name.
		InsecureSkipVerify:    true, //nolint:gosec // verified below
		VerifyPeerCertificate: VerifyServer(caDER),
	}
}

// EnrollTLS is the TLS configuration for enrolling: the worker does not
// know the CA yet, so it accepts any certificate here and checks the chain
// against the CA from the authenticated reply afterwards. When pin is set
// (a CA fingerprint), the chain must also lead to that CA.
func EnrollTLS(pin string) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true} //nolint:gosec // see the comment above
	if pin != "" {
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			for _, der := range raw {
				if Fingerprint(der) == pin {
					return VerifyServer(der)(raw, nil)
				}
			}
			return fmt.Errorf("the server's CA does not match --ca-fingerprint %s", pin)
		}
	}
	return cfg
}

// Binding returns the keying material exported from a TLS session for
// enrollment proofs.
func Binding(cs tls.ConnectionState) ([]byte, error) {
	if cs.Version < tls.VersionTLS13 {
		return nil, errors.New("enrollment needs TLS 1.3")
	}
	return cs.ExportKeyingMaterial(ExporterLabel, nil, 32)
}

func mac(token string, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, []byte(token))
	for _, p := range parts {
		var n [4]byte
		n[0], n[1], n[2], n[3] = byte(len(p)>>24), byte(len(p)>>16), byte(len(p)>>8), byte(len(p))
		m.Write(n[:])
		m.Write(p)
	}
	return m.Sum(nil)
}

// WorkerProof is the worker's proof that it knows the join token.
func WorkerProof(token string, binding []byte, name string, pub ed25519.PublicKey) []byte {
	return mac(token, []byte("stampede enroll worker"), binding, []byte(name), pub)
}

// ServerProof is the server's proof that it knows the join token and that
// cert and caDER come from it.
func ServerProof(token string, binding, cert, caDER []byte) []byte {
	return mac(token, []byte("stampede enroll server"), binding, cert, caDER)
}

// Equal compares proofs in constant time.
func Equal(a, b []byte) bool { return hmac.Equal(a, b) }

// WorkerCert assembles a worker's tls.Certificate from its key and the
// issued certificate, after checking that the certificate is for that key
// and chains to caDER for client authentication.
func WorkerCert(key ed25519.PrivateKey, certDER, caDER []byte) (tls.Certificate, *x509.Certificate, error) {
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok || !bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
		return tls.Certificate{}, nil, errors.New("the issued certificate is not for this worker's key")
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("the issued certificate does not verify: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key, Leaf: leaf}, leaf, nil
}

// PeerWorker returns the verified worker certificate of a connection, or
// an error when the peer presented none or one not for client use.
func PeerWorker(cs tls.ConnectionState) (*x509.Certificate, error) {
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return nil, errors.New("no worker certificate: enroll first (stampede worker --mtls)")
	}
	leaf := cs.VerifiedChains[0][0]
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageClientAuth {
			return leaf, nil
		}
	}
	return nil, errors.New("the certificate is not a worker certificate")
}

func parseURI(s string) (*url.URL, error) { return url.Parse(s) }
