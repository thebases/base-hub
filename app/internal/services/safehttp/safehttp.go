// Package safehttp is the outbound HTTP client for user-authored targets (workflow nodes,
// callbacks). It refuses to connect to loopback, private, link-local, CGNAT, multicast and
// other non-public addresses, checked on the resolved IP at dial time so DNS rebinding and
// redirects cannot reach them; follows at most MaxRedirects redirects; never uses an
// environment proxy (the proxy dial would hide the real target); and caps response bodies.
//
// Internal targets an operator really needs are opened with server.outboundAllowCidrs.
package safehttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	// MaxRedirects is how many redirects a request may follow.
	MaxRedirects = 3
	// MaxResponseBytes caps a response body read through ReadBody.
	MaxResponseBytes = 1 << 20
)

// ErrBlockedAddress is returned (wrapped) when the target resolves to a refused address.
var ErrBlockedAddress = errors.New("safehttp: target address is not allowed")

// ErrResponseTooLarge is returned by ReadBody when the body exceeds its limit.
var ErrResponseTooLarge = errors.New("safehttp: response body too large")

// blockedNets are refused unless an allow entry covers the address. net.IP methods cover
// loopback, RFC 1918 / fc00::/7, link-local (incl. 169.254.169.254 metadata), multicast
// and unspecified; this list adds the ranges they miss.
var blockedNets = mustParseCIDRs(
	"0.0.0.0/8",      // "this network"
	"100.64.0.0/10",  // CGNAT, often cloud-internal
	"192.0.0.0/24",   // IETF protocol assignments
	"198.18.0.0/15",  // benchmarking
	"240.0.0.0/4",    // reserved, incl. broadcast
	"64:ff9b::/96",   // NAT64: embeds an IPv4 target
	"64:ff9b:1::/48", // local-use NAT64
	"2002::/16",      // 6to4: embeds an IPv4 target
)

// Options configures a Client. Allow nil reads server.outboundAllowCidrs at dial time.
type Options struct {
	Timeout time.Duration
	Allow   []*net.IPNet
}

// NewClient returns an http.Client that only dials public addresses (plus Allow).
func NewClient(opts Options) *http.Client {
	allow := func() []*net.IPNet { return configuredAllowNets() }
	if opts.Allow != nil {
		fixed := opts.Allow
		allow = func() []*net.IPNet { return fixed }
	}
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		// Control runs per resolved address, after DNS and before connect, so a hostname
		// that resolves (or re-resolves) to a private IP is refused.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
			}
			ip := net.ParseIP(host)
			if ip == nil || !Allowed(ip, allow()) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
			return nil
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Timeout:       opts.Timeout,
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("safehttp: stopped after %d redirects", MaxRedirects)
	}
	return ValidateURL(req.URL)
}

// ValidateURL refuses anything but an absolute http(s) URL with a host and no userinfo.
func ValidateURL(u *url.URL) error {
	if u == nil {
		return errors.New("safehttp: missing url")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("safehttp: unsupported url scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return errors.New("safehttp: url has no host")
	}
	if u.User != nil {
		return errors.New("safehttp: url must not carry credentials")
	}
	return nil
}

// Allowed reports whether ip may be dialed: a public address, or one inside allow.
func Allowed(ip net.IP, allow []*net.IPNet) bool {
	for _, n := range allow {
		if n.Contains(ip) {
			return true
		}
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// ReadBody reads at most limit bytes (MaxResponseBytes when limit <= 0) and fails with
// ErrResponseTooLarge beyond that, so a hostile target cannot exhaust memory.
func ReadBody(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = MaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return body, err
	}
	if int64(len(body)) > limit {
		return body[:limit], fmt.Errorf("%w: over %d bytes", ErrResponseTooLarge, limit)
	}
	return body, nil
}

// Do validates req.URL, binds ctx and sends req with client.
func Do(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	if err := ValidateURL(req.URL); err != nil {
		return nil, err
	}
	return client.Do(req.WithContext(ctx))
}

// configuredAllowNets parses server.outboundAllowCidrs; bad entries are logged and skipped
// (fail closed: they open nothing).
func configuredAllowNets() []*net.IPNet {
	cfg := config.Get()
	if cfg == nil || len(cfg.Server.OutboundAllowCIDRs) == 0 {
		return nil
	}
	nets, bad := parseCIDRs(cfg.Server.OutboundAllowCIDRs)
	for _, entry := range bad {
		log.Warnf("safehttp: ignoring invalid server.outboundAllowCidrs entry %q", entry)
	}
	return nets
}

func parseCIDRs(entries []string) ([]*net.IPNet, []string) {
	nets := make([]*net.IPNet, 0, len(entries))
	var bad []string
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			ip := net.ParseIP(entry)
			if ip == nil {
				bad = append(bad, raw)
				continue
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(entry)
		if err != nil {
			bad = append(bad, raw)
			continue
		}
		nets = append(nets, n)
	}
	return nets, bad
}

func mustParseCIDRs(entries ...string) []*net.IPNet {
	nets, bad := parseCIDRs(entries)
	if len(bad) > 0 {
		panic(fmt.Sprintf("safehttp: bad built-in cidr %v", bad))
	}
	return nets
}
