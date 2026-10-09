package profile

import (
	"encoding/base64"
	"strings"
	"testing"
)

func sample() Profile {
	return Profile{
		V: 1, Name: "example", PrivateKey: "cHJpdmF0ZQ==", ServerKey: "c2VydmVy", ServerYgg: "200:db8::1",
		Port: 51820, IPv6: true, ClientIP4: "192.0.2.10", ClientIP6: "2001:db8::10",
		Peers: []string{"tls://192.0.2.1:1234"}, DirectPeer: "tls://192.0.2.2:1234",
	}
}

func TestParseRoundTrip(t *testing.T) {
	p := sample()
	got, err := Parse(p.Link())
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerYgg != p.ServerYgg || got.Port != p.Port || got.ClientIP4 != p.ClientIP4 ||
		got.PrivateKey != p.PrivateKey || len(got.Peers) != 1 || !got.IPv6 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseFindsLinkInsideText(t *testing.T) {
	got, err := Parse("look: " + sample().Link() + " thanks, see you")
	if err != nil || got.Name != "example" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func enc(s string) string {
	return prefix + base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"no data":       prefix,
		"bad base64":    prefix + "!!!",
		"not json":      enc("hello"),
		"array":         enc(`[1,2]`),
		"wrong v":       enc(`{"v":2,"privateKey":"a","serverKey":"b","serverYgg":"200:db8::1"}`),
		"missing key":   enc(`{"v":1,"privateKey":"a","serverYgg":"200:db8::1"}`),
		"missing ygg":   enc(`{"v":1,"privateKey":"a","serverKey":"b"}`),
		"no prefix":     strings.TrimPrefix(sample().Link(), prefix),
		"empty private": enc(`{"v":1,"privateKey":"","serverKey":"b","serverYgg":"200:db8::1"}`),
	}
	for name, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestIPv6FalseWithEmptyClientIP6(t *testing.T) {
	p := sample()
	p.IPv6, p.ClientIP6 = false, ""
	got, err := Parse(p.Link())
	if err != nil || got.IPv6 || got.ClientIP6 != "" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestTunnelConfigMapsFields(t *testing.T) {
	c := sample().TunnelConfig()
	if c.PrivateKey != "cHJpdmF0ZQ==" || c.ServerKey != "c2VydmVy" || c.ServerYgg != "200:db8::1" ||
		c.Port != 51820 || !c.IPv6 || c.ClientIP4 != "192.0.2.10" || c.Lanes != 1 {
		t.Fatalf("%+v", c)
	}
}
