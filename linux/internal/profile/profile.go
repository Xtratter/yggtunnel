// Package profile parses yggtunnel://import#… device profiles, the same format the Android app shares.
package profile

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Xtratter/yggtunnel/go/core"
)

const prefix = "yggtunnel://import#"

// Profile is a ready connection to a server: the client's own WireGuard key (already registered
// on the server), the server's key and Yggdrasil address, and peers.
type Profile struct {
	V          int      `json:"v"`
	Name       string   `json:"name,omitempty"`
	PrivateKey string   `json:"privateKey"`
	ServerKey  string   `json:"serverKey"`
	ServerYgg  string   `json:"serverYgg"`
	Port       int      `json:"port"`
	IPv6       bool     `json:"ipv6"`
	ClientIP4  string   `json:"clientIp4"`
	ClientIP6  string   `json:"clientIp6,omitempty"`
	Peers      []string `json:"peers,omitempty"`
	DirectPeer string   `json:"directPeer,omitempty"`
	WSSPeer    string   `json:"wssPeer,omitempty"`
}

// Parse finds a profile link in any text and decodes it.
func Parse(text string) (Profile, error) {
	var p Profile
	i := strings.Index(text, prefix)
	if i < 0 {
		return p, errors.New("no yggtunnel://import# link found")
	}
	rest := text[i+len(prefix):]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	})
	if end >= 0 {
		rest = rest[:end]
	}
	if rest == "" {
		return p, errors.New("the link has no data")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil {
		return p, errors.New("the link is damaged (bad base64)")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, errors.New("the link is damaged (bad JSON)")
	}
	if p.V != 1 {
		return Profile{}, errors.New("unsupported profile version")
	}
	if p.PrivateKey == "" || p.ServerKey == "" || p.ServerYgg == "" {
		return Profile{}, errors.New("the profile misses a key or the server address")
	}
	return p, nil
}

// Link encodes the profile as a yggtunnel://import# link.
func (p Profile) Link() string {
	b, _ := json.Marshal(p)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// TunnelConfig is what the core needs to attach the full tunnel.
func (p Profile) TunnelConfig() core.TunnelConfig {
	return core.TunnelConfig{
		PrivateKey: p.PrivateKey, ServerKey: p.ServerKey, ServerYgg: p.ServerYgg,
		Port: p.Port, IPv6: p.IPv6, ClientIP4: p.ClientIP4, Lanes: 1,
	}
}
