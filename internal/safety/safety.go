// Package safety keeps Stampede from being pointed at systems the user does
// not own: target classification, ownership verification, load caps and a
// per-request host policy.
package safety

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ivan825/Stampede/internal/scenario"
)

// Caps bound the load a run may generate.
type Caps struct {
	MaxRate     float64       // iterations per second (rate mode)
	MaxVUs      int           // virtual users
	MaxDuration time.Duration // planned load duration
}

// UnverifiedPublicCaps apply to public targets whose ownership has not
// been verified.
var UnverifiedPublicCaps = Caps{MaxRate: 50, MaxVUs: 50, MaxDuration: 10 * time.Minute}

// CheckPlan returns an error describing every cap the plan exceeds.
func CheckPlan(p *scenario.Plan, c Caps) error {
	var over []string
	if p.Mode == scenario.ModeRate && c.MaxRate > 0 && p.Peak() > c.MaxRate {
		over = append(over, fmt.Sprintf("rate %.0f/s exceeds the cap of %.0f/s", p.Peak(), c.MaxRate))
	}
	vus := p.VUs
	if p.Mode == scenario.ModeRate {
		vus = p.MaxVUs
	}
	if c.MaxVUs > 0 && vus > c.MaxVUs {
		over = append(over, fmt.Sprintf("%d virtual users exceeds the cap of %d", vus, c.MaxVUs))
	}
	if c.MaxDuration > 0 && p.TotalDuration() > c.MaxDuration {
		over = append(over, fmt.Sprintf("duration %s exceeds the cap of %s", p.TotalDuration(), c.MaxDuration))
	}
	if len(over) == 0 {
		return nil
	}
	return errors.New(strings.Join(over, "; "))
}

// IsPrivateAddr reports whether an address is loopback, private,
// link-local or unique-local.
func IsPrivateAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified()
}

// IsPrivateHost resolves host and reports whether every address it maps to
// is private. "localhost" and names under .localhost, .local, .internal and
// .test count as private without a lookup only if they resolve privately.
func IsPrivateHost(ctx context.Context, host string) (bool, error) {
	if a, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return IsPrivateAddr(a), nil
	}
	if host == "localhost" {
		return true, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false, err
	}
	if len(addrs) == 0 {
		return false, fmt.Errorf("%s has no addresses", host)
	}
	for _, a := range addrs {
		if !IsPrivateAddr(a) {
			return false, nil
		}
	}
	return true, nil
}

// HostPolicy decides which hosts requests may reach. The target host and
// private hosts are allowed; any other public host must be listed
// explicitly. This stops a scenario from sending load to third parties.
type HostPolicy struct {
	target string
	extra  map[string]bool
	cache  sync.Map // host -> bool
}

// NewHostPolicy allows the target host plus extra hosts.
func NewHostPolicy(targetHost string, extra []string) *HostPolicy {
	p := &HostPolicy{target: strings.ToLower(targetHost), extra: map[string]bool{}}
	for _, h := range extra {
		p.extra[strings.ToLower(h)] = true
	}
	return p
}

// Allow reports whether a request to u is permitted.
func (p *HostPolicy) Allow(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	if h == p.target || p.extra[h] {
		return true
	}
	if v, ok := p.cache.Load(h); ok {
		return v.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ok, err := IsPrivateHost(ctx, h)
	ok = ok && err == nil
	p.cache.Store(h, ok)
	return ok
}

// Ownership verification.

// WellKnownPath is where a verification token can be served.
const WellKnownPath = "/.well-known/stampede-verify.txt"

// TXTPrefix starts the DNS TXT verification record.
const TXTPrefix = "stampede-verify="

// Token derives the verification token for a host from a per-install
// secret, so the same machine always asks for the same token.
func Token(secret []byte, host string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(strings.ToLower(host)))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// InstallSecret returns this machine's verification secret, creating it on
// first use under the user config directory.
func InstallSecret() ([]byte, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "stampede", "install-secret")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return b, os.WriteFile(path, b, 0o600)
}

// Verify checks DNS TXT records on the host (and on _stampede.<host>) and
// the well-known file for the token. It returns the method that succeeded.
func Verify(ctx context.Context, base *url.URL, token string) (string, error) {
	host := base.Hostname()
	for _, name := range []string{host, "_stampede." + host} {
		txts, err := net.DefaultResolver.LookupTXT(ctx, name)
		if err != nil {
			continue
		}
		for _, t := range txts {
			if strings.TrimSpace(t) == TXTPrefix+token {
				return "dns-txt", nil
			}
		}
	}
	u := *base
	u.Path, u.RawQuery = WellKnownPath, ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err == nil {
		c := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if resp, err := c.Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == 200 && strings.Contains(string(b), token) {
				return "well-known", nil
			}
		}
	}
	return "", fmt.Errorf("ownership of %s is not verified: add a DNS TXT record %q on %s or _stampede.%s, or serve the token at %s",
		host, TXTPrefix+token, host, host, WellKnownPath)
}
