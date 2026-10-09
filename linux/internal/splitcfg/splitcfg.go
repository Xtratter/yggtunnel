// Package splitcfg validates and normalises the split-routing lists (which destinations use the
// tunnel). The daemon stores and shows the canonical form it returns.
package splitcfg

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// MaxEntries is the most subnets and the most domains a configuration may hold.
const MaxEntries = 500

// Config is the setting as the user writes it and as it is stored.
type Config struct {
	Mode    string   `json:"mode"` // "all" (default), "exclude" or "only"
	Subnets []string `json:"subnets"`
	Domains []string `json:"domains"`
}

// Normalized is the same, parsed.
type Normalized struct {
	Mode    string
	Subnets []netip.Prefix
	Domains []string // lowercase ASCII (punycode)
}

var (
	yggSpace  = netip.MustParsePrefix("200::/7") // the tunnel's own address space
	loopback4 = netip.MustParsePrefix("127.0.0.0/8")
	loopback6 = netip.MustParsePrefix("::1/128")
)

// Normalize validates c and returns the parsed form and the canonical text form.
func Normalize(c Config) (Normalized, Config, error) {
	var n Normalized
	switch c.Mode {
	case "", "all":
		n.Mode = "all"
	case "exclude", "only":
		n.Mode = c.Mode
	default:
		return n, c, fmt.Errorf("unknown routing mode %q (use all, exclude or only)", c.Mode)
	}
	if len(c.Subnets) > MaxEntries {
		return n, c, fmt.Errorf("too many subnets: %d (at most %d)", len(c.Subnets), MaxEntries)
	}
	if len(c.Domains) > MaxEntries {
		return n, c, fmt.Errorf("too many domains: %d (at most %d)", len(c.Domains), MaxEntries)
	}
	out := Config{Mode: n.Mode, Subnets: []string{}, Domains: []string{}}
	seen := map[string]bool{}
	for _, s := range c.Subnets {
		p, err := parseSubnet(s)
		if err != nil {
			return Normalized{}, c, err
		}
		if seen[p.String()] {
			continue
		}
		seen[p.String()] = true
		n.Subnets = append(n.Subnets, p)
		out.Subnets = append(out.Subnets, p.String())
	}
	seen = map[string]bool{}
	for _, d := range c.Domains {
		name, err := parseDomain(d)
		if err != nil {
			return Normalized{}, c, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		n.Domains = append(n.Domains, name)
		out.Domains = append(out.Domains, name)
	}
	return n, out, nil
}

func parseSubnet(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, errors.New("empty subnet entry")
	}
	if strings.Contains(s, "%") {
		return netip.Prefix{}, fmt.Errorf("%q: IPv6 zones (%%eth0) are not supported", s)
	}
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a valid subnet", s)
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a valid address or subnet", s)
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%q: write IPv4 addresses as IPv4, not as IPv4-mapped IPv6", s)
	}
	p = p.Masked()
	switch {
	case p.Bits() == 0:
		return netip.Prefix{}, fmt.Errorf("%q is a default route: use the mode \"all\" instead", s)
	case p.Overlaps(yggSpace):
		return netip.Prefix{}, fmt.Errorf("%q overlaps 200::/7, the tunnel's own address space", s)
	case p.Overlaps(loopback4) || p.Overlaps(loopback6):
		return netip.Prefix{}, fmt.Errorf("%q overlaps the loopback range", s)
	}
	return p, nil
}

func parseDomain(d string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(d))
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return "", errors.New("empty domain entry")
	}
	if strings.Contains(name, "*") {
		return "", fmt.Errorf("%q: wildcards are not supported, list each name", d)
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", fmt.Errorf("%q is an address: put it in the subnet list", d)
	}
	ascii, err := idna.Lookup.ToASCII(name)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid domain name", d)
	}
	if len(ascii) > 253 {
		return "", fmt.Errorf("%q is too long (at most 253 characters)", d)
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("%q: a domain needs at least two labels (example.com)", d)
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 {
			return "", fmt.Errorf("%q: every label must be 1 to 63 characters", d)
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return "", fmt.Errorf("%q: a label cannot start or end with a dash", d)
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return "", fmt.Errorf("%q has a character that is not allowed in a name", d)
			}
		}
	}
	return ascii, nil
}
