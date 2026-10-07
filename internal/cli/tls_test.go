package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/server"
)

func selfSigned(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return certFile, keyFile
}

func TestUITLSFlags(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cert, key := selfSigned(t)
	for _, tc := range []struct {
		name string
		f    serverFlags
		err  string
		tls  bool
	}{
		{name: "plain", f: serverFlags{}},
		{name: "cert", f: serverFlags{uiCert: cert, uiKey: key, redirectAddr: ":0"}, tls: true},
		{name: "cert without key", f: serverFlags{uiCert: cert}, err: "go together"},
		{name: "both", f: serverFlags{uiCert: cert, uiKey: key, acmeDomains: []string{"x.example"}}, err: "alternatives"},
		{name: "acme", f: serverFlags{acmeDomains: []string{"stampede.example"}, acmeCache: t.TempDir()}, tls: true},
		{name: "redirect without tls", f: serverFlags{redirectAddr: ":80"}, err: "needs HTTPS"},
	} {
		var cfg server.Config
		err := tc.f.uiTLS(&cfg, log)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v", tc.name, err)
			}
			continue
		}
		if err != nil || (cfg.TLS != nil) != tc.tls || cfg.SecureCookies != tc.tls {
			t.Errorf("%s: err %v tls %v secure %v", tc.name, err, cfg.TLS != nil, cfg.SecureCookies)
		}
	}
}
