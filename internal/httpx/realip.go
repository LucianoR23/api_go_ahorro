package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// CloudflareRanges: Cloudflare's published edge ranges
// (https://www.cloudflare.com/ips-v4 and /ips-v6, checked 2026-11-06). Used
// when CLOUDFLARE_RANGES is not set.
var CloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// ParsePrefixes parses CIDRs (or single IPs) from config.
func ParsePrefixes(list []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("IP inválida %q: %w", s, err)
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("rango inválido %q: %w", s, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func inAny(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// RealIP sets r.RemoteAddr to the client's IP without trusting headers a
// client can forge (plan 0.6). Chain: client -> Cloudflare -> Traefik -> API.
//   - The direct peer is trusted only if it is in trustedProxies (Traefik's
//     network); otherwise its own address is the client.
//   - From a trusted peer, X-Forwarded-For is read right to left, skipping
//     trusted proxies; the first other address connected to Traefik.
//   - If that address is a Cloudflare edge, CF-Connecting-IP holds the
//     client. If not (someone reaching the origin directly), that address is
//     the client and any CF-Connecting-IP it sent is ignored.
func RealIP(trustedProxies, cloudflare []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := clientAddr(r, trustedProxies, cloudflare); ip.IsValid() {
				r.RemoteAddr = ip.String()
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientAddr(r *http.Request, trusted, cloudflare []netip.Prefix) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, ok := parseAddr(host)
	if !ok || !inAny(peer, trusted) {
		return peer
	}
	hop := peer
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(strings.Join(xff, ","), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			a, ok := parseAddr(parts[i])
			if !ok {
				break
			}
			hop = a
			if !inAny(a, trusted) {
				break
			}
		}
	}
	if inAny(hop, cloudflare) {
		if cf, ok := parseAddr(r.Header.Get("CF-Connecting-IP")); ok {
			return cf
		}
	}
	return hop
}
