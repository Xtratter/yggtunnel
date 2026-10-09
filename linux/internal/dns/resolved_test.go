package dns

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

type call struct {
	method string
	args   []any
}

type fake struct {
	calls  []call
	failOn string
}

func (f *fake) Call(method string, args ...any) error {
	f.calls = append(f.calls, call{method, args})
	if f.failOn != "" && strings.HasSuffix(method, f.failOn) {
		return errors.New("fail " + f.failOn)
	}
	return nil
}

var servers = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}

func TestSetCallsExpectedMethods(t *testing.T) {
	f := &fake{}
	if err := Set(f, 7, servers); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range f.calls {
		names = append(names, c.method[strings.LastIndex(c.method, ".")+1:])
		if c.args[0] != int32(7) {
			t.Errorf("%s: first argument %v, want link index 7", c.method, c.args[0])
		}
	}
	want := "SetLinkDNS SetLinkDomains SetLinkDefaultRoute"
	if strings.Join(names, " ") != want {
		t.Fatalf("calls %v, want %s", names, want)
	}
	addrs := f.calls[0].args[1].([]Address)
	if len(addrs) != 2 || addrs[0].Family != 2 || len(addrs[0].Bytes) != 4 || addrs[0].Bytes[0] != 1 || addrs[1].Bytes[0] != 8 {
		t.Fatalf("addresses %+v", addrs)
	}
	doms := f.calls[1].args[1].([]Domain)
	if len(doms) != 1 || doms[0].Name != "." || !doms[0].RoutingOnly {
		t.Fatalf("domains %+v", doms)
	}
	if f.calls[2].args[1] != true {
		t.Fatalf("default route arg %v", f.calls[2].args[1])
	}
}

func TestSetIPv6Family(t *testing.T) {
	f := &fake{}
	Set(f, 3, []netip.Addr{netip.MustParseAddr("2001:db8::53")})
	a := f.calls[0].args[1].([]Address)
	if a[0].Family != 10 || len(a[0].Bytes) != 16 {
		t.Fatalf("%+v", a)
	}
}

func TestSetStopsOnFirstError(t *testing.T) {
	f := &fake{failOn: "SetLinkDNS"}
	if err := Set(f, 1, servers); err == nil {
		t.Fatal("expected error")
	}
	if len(f.calls) != 1 {
		t.Fatalf("continued after the error: %d calls", len(f.calls))
	}
}

func TestSetWithNoServersIsError(t *testing.T) {
	if err := Set(&fake{}, 1, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestRevertCallsRevertLink(t *testing.T) {
	f := &fake{}
	if err := Revert(f, 9); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.HasSuffix(f.calls[0].method, ".RevertLink") || f.calls[0].args[0] != int32(9) {
		t.Fatalf("%+v", f.calls)
	}
}

func TestSystemConnErrorMentionsResolved(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "unix:path=/nonexistent/yggtunnel-test-bus")
	_, err := SystemConn()
	if err == nil || !strings.Contains(err.Error(), "systemd-resolved") {
		t.Fatalf("err=%v", err)
	}
}

func TestSetWithDomainsAndNoDefaultRoute(t *testing.T) {
	f := &fake{}
	if err := SetWith(f, 7, servers, Options{DefaultRoute: false, Domains: []string{"example.com", "example.net"}}); err != nil {
		t.Fatal(err)
	}
	doms := f.calls[1].args[1].([]Domain)
	if len(doms) != 2 || doms[0] != (Domain{Name: "example.com", RoutingOnly: true}) || doms[1] != (Domain{Name: "example.net", RoutingOnly: true}) {
		t.Fatalf("domains %+v", doms)
	}
	if f.calls[2].args[1] != false {
		t.Fatalf("default route arg %v, want false", f.calls[2].args[1])
	}
}

func TestSetWithDefaultRouteKeepsTildeDot(t *testing.T) {
	f := &fake{}
	if err := SetWith(f, 7, servers, Options{DefaultRoute: true}); err != nil {
		t.Fatal(err)
	}
	doms := f.calls[1].args[1].([]Domain)
	if len(doms) != 1 || doms[0] != (Domain{Name: ".", RoutingOnly: true}) || f.calls[2].args[1] != true {
		t.Fatalf("domains %+v default %v", doms, f.calls[2].args[1])
	}
}

func TestSetWithEmptyDomainsAndNoDefaultRoute(t *testing.T) {
	f := &fake{}
	if err := SetWith(f, 7, servers, Options{}); err != nil {
		t.Fatal(err)
	}
	doms := f.calls[1].args[1].([]Domain)
	if len(doms) != 0 || f.calls[2].args[1] != false {
		t.Fatalf("domains %+v default %v: nothing should be routed to the tunnel's DNS", doms, f.calls[2].args[1])
	}
}

func TestSetMeansDefaultRoute(t *testing.T) {
	a, b := &fake{}, &fake{}
	Set(a, 3, servers)
	SetWith(b, 3, servers, Options{DefaultRoute: true})
	if len(a.calls) != len(b.calls) {
		t.Fatalf("%d vs %d calls", len(a.calls), len(b.calls))
	}
}
