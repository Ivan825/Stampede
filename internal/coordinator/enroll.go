package coordinator

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	workerv1 "github.com/Ivan825/Stampede/gen/stampede/worker/v1"
	"github.com/Ivan825/Stampede/internal/pki"
)

// DefaultCertValidity is how long an enrolled worker's certificate lasts.
// Workers renew at half-life, so rotating the join token locks every
// worker out within this time.
const DefaultCertValidity = 24 * time.Hour

func tlsState(ctx context.Context) (credentials.TLSInfo, bool) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return credentials.TLSInfo{}, false
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	return info, ok
}

// Enroll implements WorkerService.
func (c *Coordinator) Enroll(ctx context.Context, req *workerv1.EnrollRequest) (*workerv1.EnrollResponse, error) {
	ca := c.cfg.CA
	if ca == nil {
		return nil, status.Error(codes.FailedPrecondition, "this server has no built-in CA: start it with --worker-mtls, or connect without --mtls")
	}
	if c.cfg.JoinToken == "" {
		return nil, status.Error(codes.Unauthenticated, "this server has no join token configured, so it accepts no workers")
	}
	info, ok := tlsState(ctx)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "enrollment needs TLS")
	}
	binding, err := pki.Binding(info.State)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	pub := ed25519.PublicKey(req.GetPublicKey())
	if len(pub) != ed25519.PublicKeySize {
		return nil, status.Error(codes.InvalidArgument, "public_key must be a 32-byte Ed25519 key")
	}
	name := req.GetName()
	if name == "" || len(name) > 128 {
		return nil, status.Error(codes.InvalidArgument, "name must be 1 to 128 characters")
	}
	if !pki.Equal(req.GetProof(), pki.WorkerProof(c.cfg.JoinToken, binding, name, pub)) {
		c.log.Warn("enrollment refused: the proof does not match the join token", "name", name)
		return nil, status.Error(codes.Unauthenticated, "invalid join token")
	}
	var idb [8]byte
	if _, err := rand.Read(idb[:]); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	id := hex.EncodeToString(idb[:])
	validity := c.cfg.CertValidity
	if validity <= 0 {
		validity = DefaultCertValidity
	}
	cert, err := ca.IssueWorker(name, id, pub, validity)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	c.log.Info("worker enrolled", "name", name, "certificate", id, "valid", validity)
	return &workerv1.EnrollResponse{
		Certificate: cert, CaCertificate: ca.DER, WorkerId: id,
		Proof: pki.ServerProof(c.cfg.JoinToken, binding, cert, ca.DER),
	}, nil
}

// checkWorkerCert enforces mutual TLS on the Connect stream when the
// server has a built-in CA.
func (c *Coordinator) checkWorkerCert(ctx context.Context) (string, error) {
	if c.cfg.CA == nil {
		return "", nil
	}
	info, ok := tlsState(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "mutual TLS is required")
	}
	leaf, err := pki.PeerWorker(info.State)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, err.Error())
	}
	return leaf.Subject.CommonName, nil
}
