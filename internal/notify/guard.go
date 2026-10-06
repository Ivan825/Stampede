package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateDestination is returned for a destination on a private,
// loopback, link-local or otherwise internal address when the channel
// does not allow private destinations.
var ErrPrivateDestination = errors.New("destination is a private, loopback or link-local address")

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this" network
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, broadcast
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("fec0::/10"),      // deprecated site-local
	netip.MustParsePrefix("100::/64"),       // discard-only
}

// IsInternal reports whether a is an address webhooks must not reach
// without an explicit opt-in: loopback, private, link-local (including
// cloud metadata at 169.254.169.254), unspecified, multicast and reserved
// ranges, also when wrapped in IPv4-mapped, NAT64 or 6to4 IPv6 addresses.
func IsInternal(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	if a.Is6() {
		b := a.As16()
		switch {
		case b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b: // NAT64
			return IsInternal(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		case b[0] == 0x20 && b[1] == 0x02: // 6to4
			return IsInternal(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	return false
}

// CheckURL validates a destination URL: http or https, a host, no
// credentials, and, unless allowPrivate, a host that does not resolve to
// an internal address. The check is repeated on every connection (see
// Client), so a DNS change cannot bypass it.
func CheckURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("the URL must start with https:// (or http://)")
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("the URL has no host")
	}
	if u.User != nil {
		return nil, errors.New("put credentials in the URL path or use a signing secret, not user:password@")
	}
	if allowPrivate {
		return u, nil
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		if IsInternal(a) {
			return nil, ErrPrivateDestination
		}
		return u, nil
	}
	if h := strings.ToLower(strings.TrimSuffix(host, ".")); h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".local") {
		return nil, ErrPrivateDestination
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(rctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, a := range addrs {
		if IsInternal(a) {
			return nil, ErrPrivateDestination
		}
	}
	return u, nil
}

// Client returns an HTTP client for deliveries. Unless allowPrivate, every
// connection is checked after DNS resolution, against the address actually
// dialled, so neither DNS rebinding nor a redirect can reach an internal
// address. Proxies from the environment are ignored for the same reason,
// and redirects are not followed.
func Client(allowPrivate bool, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			a, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("unexpected dial address %q", address)
			}
			if IsInternal(a) {
				return ErrPrivateDestination
			}
			return nil
		}
	}
	t := &http.Transport{
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
