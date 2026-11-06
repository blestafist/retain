// Package origin parses and compares HTTP origins and Host authorities.
package origin

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Origin is a normalized HTTP or HTTPS origin. Its hostname is lowercased and
// its port is always explicit, including the scheme's default port.
type Origin struct {
	scheme   string
	hostname string
	port     uint16
}

// Parse accepts an absolute HTTP(S) origin without userinfo, path, query, or
// fragment.
func Parse(raw string) (Origin, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.Contains(raw, ",") {
		return Origin{}, fmt.Errorf("invalid origin")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Origin{}, fmt.Errorf("invalid origin: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if !u.IsAbs() || (scheme != "http" && scheme != "https") || u.Opaque != "" || u.User != nil || u.Host == "" {
		return Origin{}, fmt.Errorf("origin must be an absolute HTTP(S) origin")
	}
	if u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") {
		return Origin{}, fmt.Errorf("origin must not contain a path, query, or fragment")
	}
	if strings.HasSuffix(u.Host, ":") {
		return Origin{}, fmt.Errorf("origin has an empty port")
	}

	hostname := u.Hostname()
	if hostname == "" {
		return Origin{}, fmt.Errorf("origin hostname must not be empty")
	}
	port := defaultPort(scheme)
	if rawPort := u.Port(); rawPort != "" {
		parsedPort, err := strconv.ParseUint(rawPort, 10, 16)
		if err != nil || parsedPort == 0 {
			return Origin{}, fmt.Errorf("origin port must be between 1 and 65535")
		}
		port = uint16(parsedPort)
	}

	return Origin{
		scheme:   scheme,
		hostname: normalizeHostname(hostname),
		port:     port,
	}, nil
}

// Matches reports whether raw is the same normalized origin.
func (o Origin) Matches(raw string) bool {
	other, err := Parse(raw)
	return err == nil && o == other
}

// MatchesHost reports whether a request Host authority has this origin's
// hostname and effective port. The expected scheme supplies the default port.
func (o Origin) MatchesHost(host string) bool {
	if host == "" {
		return false
	}
	requestOrigin, err := Parse(o.scheme + "://" + host)
	return err == nil && o.hostname == requestOrigin.hostname && o.port == requestOrigin.port
}

func defaultPort(scheme string) uint16 {
	if scheme == "https" {
		return 443
	}
	return 80
}

func normalizeHostname(hostname string) string {
	if address, err := netip.ParseAddr(hostname); err == nil {
		return address.String()
	}
	return strings.ToLower(hostname)
}
