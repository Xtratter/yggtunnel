package splitcfg

import (
	"fmt"
	"math/rand"
	"net/netip"
	"strings"
	"testing"
)

func TestNormalizeAcceptsAndCanonicalises(t *testing.T) {
	n, c, err := Normalize(Config{
		Mode:    "exclude",
		Subnets: []string{"203.0.113.0/24", " 198.51.100.7 ", "2001:db8::1", "203.0.113.5/24", "203.0.113.0/24"},
		Domains: []string{"Example.COM", "example.net.", "Bücher.example", "example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSub := []string{"203.0.113.0/24", "198.51.100.7/32", "2001:db8::1/128"}
	if strings.Join(c.Subnets, ",") != strings.Join(wantSub, ",") {
		t.Fatalf("subnets %v, want %v (masked, bare addresses widened, duplicates collapsed, order kept)", c.Subnets, wantSub)
	}
	wantDom := []string{"example.com", "example.net", "xn--bcher-kva.example"}
	if strings.Join(c.Domains, ",") != strings.Join(wantDom, ",") {
		t.Fatalf("domains %v, want %v", c.Domains, wantDom)
	}
	if n.Mode != "exclude" || len(n.Subnets) != 3 || n.Subnets[0] != netip.MustParsePrefix("203.0.113.0/24") || len(n.Domains) != 3 {
		t.Fatalf("normalized %+v", n)
	}
}

func TestEmptyModeMeansAll(t *testing.T) {
	n, c, err := Normalize(Config{})
	if err != nil || n.Mode != "all" || c.Mode != "all" {
		t.Fatalf("%+v %+v %v", n, c, err)
	}
}

func TestModeAllStillValidatesLists(t *testing.T) {
	if _, _, err := Normalize(Config{Mode: "all", Subnets: []string{"::/0"}}); err == nil {
		t.Fatal("a bad list must be rejected whatever the mode")
	}
}

func TestNormalizeRejects(t *testing.T) {
	long := strings.Repeat("a", 64)
	tooLong := strings.Repeat("abcdefghi.", 26) + "com" // 263 characters
	var many501 []string
	for i := 0; i < 501; i++ {
		many501 = append(many501, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}
	var domains501 []string
	for i := 0; i < 501; i++ {
		domains501 = append(domains501, fmt.Sprintf("host%d.example.com", i))
	}
	cases := []struct {
		name string
		c    Config
		want string // part of the error text
	}{
		{"default v4", Config{Mode: "only", Subnets: []string{"0.0.0.0/0"}}, "default"},
		{"default v6", Config{Mode: "only", Subnets: []string{"::/0"}}, "default"},
		{"overlaps ygg", Config{Mode: "only", Subnets: []string{"200::/7"}}, "200::/7"},
		{"inside ygg", Config{Mode: "only", Subnets: []string{"200:db8::/32"}}, "200::/7"},
		{"covers ygg", Config{Mode: "only", Subnets: []string{"::/1"}}, "200::/7"},
		{"loopback v4", Config{Mode: "only", Subnets: []string{"127.0.0.0/8"}}, "loopback"},
		{"loopback addr", Config{Mode: "only", Subnets: []string{"127.0.0.1"}}, "loopback"},
		{"loopback v6", Config{Mode: "only", Subnets: []string{"::1"}}, "loopback"},
		{"bad octet", Config{Mode: "only", Subnets: []string{"256.1.1.1"}}, "256.1.1.1"},
		{"bad bits", Config{Mode: "only", Subnets: []string{"1.2.3.4/33"}}, "1.2.3.4/33"},
		{"zone", Config{Mode: "only", Subnets: []string{"fe80::1%eth0"}}, "zone"},
		{"mapped", Config{Mode: "only", Subnets: []string{"::ffff:1.2.3.4"}}, "IPv4"},
		{"empty subnet", Config{Mode: "only", Subnets: []string{"  "}}, "empty"},
		{"wildcard", Config{Mode: "only", Domains: []string{"*.example.com"}}, "wildcard"},
		{"space", Config{Mode: "only", Domains: []string{"exa mple.com"}}, "exa mple.com"},
		{"empty label", Config{Mode: "only", Domains: []string{"example..com"}}, "example..com"},
		{"leading dash", Config{Mode: "only", Domains: []string{"-bad.example"}}, "-bad.example"},
		{"long label", Config{Mode: "only", Domains: []string{long + ".example"}}, "63"},
		{"long name", Config{Mode: "only", Domains: []string{tooLong}}, "253"},
		{"bare tld", Config{Mode: "only", Domains: []string{"com"}}, "two"},
		{"an address as a domain", Config{Mode: "only", Domains: []string{"203.0.113.5"}}, "subnet"},
		{"empty domain", Config{Mode: "only", Domains: []string{""}}, "empty"},
		{"501 subnets", Config{Mode: "only", Subnets: many501}, "500"},
		{"501 domains", Config{Mode: "only", Domains: domains501}, "500"},
		{"unknown mode", Config{Mode: "sometimes"}, "mode"},
	}
	for _, c := range cases {
		_, _, err := Normalize(c.c)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}

func TestExactlyFiveHundredIsAccepted(t *testing.T) {
	var subnets, domains []string
	for i := 0; i < 500; i++ {
		subnets = append(subnets, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
		domains = append(domains, fmt.Sprintf("host%d.example.com", i))
	}
	if _, _, err := Normalize(Config{Mode: "only", Subnets: subnets, Domains: domains}); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeIsIdempotent(t *testing.T) {
	_, c1, err := Normalize(Config{Mode: "only", Subnets: []string{"203.0.113.9/24", "2001:db8::5"}, Domains: []string{"Example.ORG."}})
	if err != nil {
		t.Fatal(err)
	}
	_, c2, err := Normalize(c1)
	if err != nil || fmt.Sprint(c1) != fmt.Sprint(c2) {
		t.Fatalf("%v vs %v (%v)", c1, c2, err)
	}
}

func TestNormalizeNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("abcxyz019.-:/%*_ \t\n\xff\xc3\xbc[]@")
	for i := 0; i < 5000; i++ {
		b := make([]byte, rng.Intn(40))
		for j := range b {
			b[j] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(b)
		Normalize(Config{Mode: "only", Subnets: []string{s}, Domains: []string{s}})
	}
}
