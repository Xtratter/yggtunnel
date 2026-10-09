package daemon

import (
	"net"
	"net/url"
	"strings"
)

// resolvePeers replaces host names in peer URIs with an IP address (IPv4 preferred, in case the
// machine has no IPv6) and keeps the name as ?sni= for the schemes that verify it. URIs it cannot
// resolve or parse are passed on unchanged.
func resolvePeers(peers []string, lookup func(string) ([]string, error)) []string {
	out := make([]string, len(peers))
	for i, p := range peers {
		out[i] = resolvePeer(p, lookup)
	}
	return out
}

func resolvePeer(peer string, lookup func(string) ([]string, error)) string {
	u, err := url.Parse(peer)
	if err != nil || u.Hostname() == "" || net.ParseIP(u.Hostname()) != nil {
		return peer
	}
	addrs, err := lookup(u.Hostname())
	if err != nil || len(addrs) == 0 {
		return peer
	}
	ip := addrs[0]
	for _, a := range addrs {
		if p := net.ParseIP(a); p != nil && p.To4() != nil {
			ip = a
			break
		}
	}
	name := u.Hostname()
	host := ip
	if strings.Contains(ip, ":") {
		host = "[" + ip + "]"
	}
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	u.Host = host
	switch u.Scheme {
	case "tls", "wss", "quic":
		if u.Query().Get("sni") == "" {
			if u.RawQuery != "" {
				u.RawQuery += "&"
			}
			u.RawQuery += "sni=" + url.QueryEscape(name)
		}
	}
	return u.String()
}
