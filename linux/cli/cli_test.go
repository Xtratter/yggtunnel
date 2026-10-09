package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
)

type stub struct {
	got    []ipc.Request
	extra  map[string]any // merged into the status answer
	setErr string         // when set, `set` fails with this text
}

func (s *stub) Handle(_ context.Context, _ ipc.Peer, req ipc.Request) (any, error) {
	s.got = append(s.got, req)
	switch req.Cmd {
	case "status":
		m := map[string]any{"state": "connected", "error": "", "profile": map[string]any{"name": "example", "serverYgg": "200:db8::2"}}
		for k, v := range s.extra {
			m[k] = v
		}
		return m, nil
	case "set":
		if s.setErr != "" {
			return nil, errors.New(s.setErr)
		}
		return nil, nil
	case "fail":
		return nil, errors.New("boom")
	case "up":
		return nil, errors.New("not authorized by polkit")
	case "version":
		return "0.1.0", nil
	case "log":
		return "line one\nline two", nil
	}
	return nil, nil
}

func serve(t *testing.T) (string, *stub) {
	t.Helper()
	u, _ := user.Current()
	g, _ := user.LookupGroupId(u.Gid)
	path := filepath.Join(t.TempDir(), "s.sock")
	s := &stub{}
	srv, err := ipc.Listen(path, g.Name, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return path, s
}

func do(t *testing.T, sock, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"--socket", sock}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestCLIStatusPrintsState(t *testing.T) {
	sock, _ := serve(t)
	code, out, _ := do(t, sock, "", "status")
	if code != 0 || !strings.Contains(out, "connected") || !strings.Contains(out, "example") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCLIStatusJSON(t *testing.T) {
	sock, _ := serve(t)
	_, out, _ := do(t, sock, "", "status", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["state"] != "connected" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestCLIImportReadsStdin(t *testing.T) {
	sock, s := serve(t)
	code, _, errs := do(t, sock, "look yggtunnel://import#abc ok\n", "import", "-")
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	var a struct{ Link string }
	json.Unmarshal(s.got[0].Args, &a)
	if s.got[0].Cmd != "import" || !strings.Contains(a.Link, "yggtunnel://import#abc") {
		t.Fatalf("sent %+v", s.got[0])
	}
}

func TestCLIImportReadsFile(t *testing.T) {
	sock, s := serve(t)
	f := filepath.Join(t.TempDir(), "p.txt")
	os.WriteFile(f, []byte("yggtunnel://import#fromfile"), 0o600)
	if code, _, errs := do(t, sock, "", "import", f); code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	if !strings.Contains(string(s.got[0].Args), "fromfile") {
		t.Fatalf("sent %s", s.got[0].Args)
	}
}

func TestCLIImportLinkAsArgumentRefused(t *testing.T) {
	sock, s := serve(t)
	code, _, errs := do(t, sock, "", "import", "yggtunnel://import#inline")
	if code != 1 || !strings.Contains(errs, "stdin") || len(s.got) != 0 {
		t.Fatalf("code=%d err=%q sent=%d", code, errs, len(s.got))
	}
}

func TestCLIExitCodeOnError(t *testing.T) {
	sock, _ := serve(t)
	code, _, errs := do(t, sock, "", "up")
	if code != 1 || !strings.Contains(errs, "not authorized") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestCLIUnknownCommand(t *testing.T) {
	sock, _ := serve(t)
	if code, _, errs := do(t, sock, "", "frobnicate"); code != 2 || !strings.Contains(errs, "usage") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestCLINoCommand(t *testing.T) {
	if code, _, errs := do(t, "/nonexistent", ""); code != 2 || !strings.Contains(errs, "usage") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestCLIDaemonNotRunning(t *testing.T) {
	code, _, errs := do(t, filepath.Join(t.TempDir(), "none.sock"), "", "status")
	if code != 1 || !strings.Contains(errs, "yggtunneld") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestCLIVersionAndLog(t *testing.T) {
	sock, _ := serve(t)
	if _, out, _ := do(t, sock, "", "version"); strings.TrimSpace(out) != "0.1.0" {
		t.Fatalf("version %q", out)
	}
	if _, out, _ := do(t, sock, "", "log"); !strings.Contains(out, "line two") {
		t.Fatalf("log %q", out)
	}
}

func TestCLIKillSwitchOnSendsSet(t *testing.T) {
	sock, s := serve(t)
	if code, _, errs := do(t, sock, "", "killswitch", "on"); code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	if s.got[0].Cmd != "set" || string(s.got[0].Args) != `{"killSwitch":true}` {
		t.Fatalf("sent %s %s", s.got[0].Cmd, s.got[0].Args)
	}
}

func TestCLIKillSwitchOffSendsSet(t *testing.T) {
	sock, s := serve(t)
	do(t, sock, "", "killswitch", "off")
	if string(s.got[0].Args) != `{"killSwitch":false}` {
		t.Fatalf("sent %s", s.got[0].Args)
	}
}

func TestCLILanOffSendsSet(t *testing.T) {
	sock, s := serve(t)
	if code, _, errs := do(t, sock, "", "lan", "off"); code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	if s.got[0].Cmd != "set" || string(s.got[0].Args) != `{"allowLan":false}` {
		t.Fatalf("sent %s %s", s.got[0].Cmd, s.got[0].Args)
	}
}

func TestCLIKillSwitchBadArgument(t *testing.T) {
	sock, s := serve(t)
	for _, args := range [][]string{{"killswitch"}, {"killswitch", "maybe"}, {"killswitch", "on", "off"}, {"lan"}, {"lan", "1"}} {
		code, _, errs := do(t, sock, "", args...)
		if code != 2 || !strings.Contains(errs, "usage") {
			t.Errorf("%v: code=%d err=%q", args, code, errs)
		}
	}
	if len(s.got) != 0 {
		t.Fatalf("a bad command reached the daemon: %v", s.got)
	}
}

func TestCLIStatusShowsKillSwitchStates(t *testing.T) {
	cases := []struct {
		name   string
		extra  map[string]any
		want   []string
		absent []string
	}{
		{"off", map[string]any{"settings": map[string]any{"killSwitch": false, "allowLan": true}, "killSwitchActive": false},
			[]string{"Kill switch: off"}, []string{"Local network"}},
		{"armed", map[string]any{"settings": map[string]any{"killSwitch": true, "allowLan": true}, "killSwitchActive": false},
			[]string{"Kill switch: armed", "Local network: allowed"}, nil},
		{"active", map[string]any{"settings": map[string]any{"killSwitch": true, "allowLan": false}, "killSwitchActive": true},
			[]string{"Kill switch: active", "Local network: dropped"}, nil},
	}
	for _, c := range cases {
		sock, s := serve(t)
		s.extra = c.extra
		_, out, _ := do(t, sock, "", "status")
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, w, out)
			}
		}
		for _, a := range c.absent {
			if strings.Contains(out, a) {
				t.Errorf("%s: output has %q:\n%s", c.name, a, out)
			}
		}
	}
}

// ---- split routing -----------------------------------------------------------------------------

func splitStatus(mode string, subnets, domains []string) map[string]any {
	return map[string]any{"settings": map[string]any{"killSwitch": false, "allowLan": true,
		"split": map[string]any{"mode": mode, "subnets": subnets, "domains": domains}}}
}

func lastSet(t *testing.T, s *stub) (mode string, subnets, domains []string) {
	t.Helper()
	for i := len(s.got) - 1; i >= 0; i-- {
		if s.got[i].Cmd == "set" {
			var a struct {
				Split struct {
					Mode    string   `json:"mode"`
					Subnets []string `json:"subnets"`
					Domains []string `json:"domains"`
				} `json:"split"`
			}
			if err := json.Unmarshal(s.got[i].Args, &a); err != nil {
				t.Fatal(err)
			}
			return a.Split.Mode, a.Split.Subnets, a.Split.Domains
		}
	}
	t.Fatalf("no set was sent: %v", s.got)
	return
}

func sets(s *stub) int {
	n := 0
	for _, r := range s.got {
		if r.Cmd == "set" {
			n++
		}
	}
	return n
}

func TestCLISplitModeSendsSetWithTheWholeObject(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("exclude", []string{"203.0.113.0/24"}, []string{"example.com"})
	if code, _, errs := do(t, sock, "", "split", "mode", "only"); code != 0 {
		t.Fatalf("code=%d err=%s", code, errs)
	}
	m, sub, dom := lastSet(t, s)
	if m != "only" || len(sub) != 1 || sub[0] != "203.0.113.0/24" || len(dom) != 1 || dom[0] != "example.com" {
		t.Fatalf("%s %v %v", m, sub, dom)
	}
}

func TestCLISplitAddSubnetKeepsExisting(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("exclude", []string{"203.0.113.0/24"}, nil)
	do(t, sock, "", "split", "add", "198.51.100.0/24")
	m, sub, _ := lastSet(t, s)
	if m != "exclude" || len(sub) != 2 || sub[1] != "198.51.100.0/24" {
		t.Fatalf("%s %v", m, sub)
	}
}

func TestCLISplitAddBareAddressIsASubnet(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", nil, nil)
	do(t, sock, "", "split", "add", "192.0.2.7")
	_, sub, dom := lastSet(t, s)
	if len(sub) != 1 || sub[0] != "192.0.2.7" || len(dom) != 0 {
		t.Fatalf("%v %v", sub, dom)
	}
}

func TestCLISplitAddDomain(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", []string{"203.0.113.0/24"}, []string{"example.com"})
	do(t, sock, "", "split", "add", "Example.NET")
	_, sub, dom := lastSet(t, s)
	if len(sub) != 1 || len(dom) != 2 || dom[1] != "Example.NET" {
		t.Fatalf("%v %v (the daemon normalises the case)", sub, dom)
	}
}

func TestCLISplitAddExistingIsNoop(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", []string{"203.0.113.0/24"}, []string{"example.com"})
	_, out, _ := do(t, sock, "", "split", "add", "EXAMPLE.com")
	do(t, sock, "", "split", "add", "203.0.113.0/24")
	if sets(s) != 0 || !strings.Contains(out, "already") {
		t.Fatalf("sets=%d out=%q", sets(s), out)
	}
}

func TestCLISplitRemove(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", []string{"203.0.113.0/24", "198.51.100.0/24"}, []string{"example.com", "example.net"})
	do(t, sock, "", "split", "remove", "198.51.100.0/24")
	do(t, sock, "", "split", "remove", "Example.com")
	// the stub's status never changes, so each command starts from the full lists: look at both sets
	var firstSub, lastDom []string
	n := 0
	for _, r := range s.got {
		if r.Cmd != "set" {
			continue
		}
		var a struct {
			Split struct{ Subnets, Domains []string }
		}
		json.Unmarshal(r.Args, &a)
		if n == 0 {
			firstSub = a.Split.Subnets
		}
		lastDom = a.Split.Domains
		n++
	}
	if len(firstSub) != 1 || firstSub[0] != "203.0.113.0/24" {
		t.Fatalf("after removing 198.51.100.0/24: %v", firstSub)
	}
	if len(lastDom) != 1 || lastDom[0] != "example.net" {
		t.Fatalf("after removing Example.com: %v", lastDom)
	}
	if sets(s) != 2 {
		t.Fatalf("sets=%d", sets(s))
	}
}

func TestCLISplitRemoveBareAddressMatchesItsHostRoute(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", []string{"192.0.2.7/32"}, nil)
	do(t, sock, "", "split", "remove", "192.0.2.7")
	if _, sub, _ := lastSet(t, s); len(sub) != 0 {
		t.Fatalf("%v", sub)
	}
}

func TestCLISplitRemoveMissingIsNoop(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("only", []string{"203.0.113.0/24"}, nil)
	code, out, _ := do(t, sock, "", "split", "remove", "example.org")
	if code != 0 || sets(s) != 0 || !strings.Contains(out, "not in the list") {
		t.Fatalf("code=%d sets=%d out=%q", code, sets(s), out)
	}
}

func TestCLISplitBadArguments(t *testing.T) {
	sock, s := serve(t)
	for _, args := range [][]string{{"split"}, {"split", "mode"}, {"split", "mode", "maybe"}, {"split", "add"}, {"split", "add", "a", "b"},
		{"split", "remove"}, {"split", "show", "x"}, {"split", "frobnicate"}} {
		code, _, errs := do(t, sock, "", args...)
		if code != 2 || !strings.Contains(errs, "usage") {
			t.Errorf("%v: code=%d err=%q", args, code, errs)
		}
	}
	if len(s.got) != 0 {
		t.Fatalf("a bad command reached the daemon: %v", s.got)
	}
}

func TestCLISplitShowListsEntries(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("exclude", []string{"203.0.113.0/24"}, []string{"example.com", "example.net"})
	_, out, _ := do(t, sock, "", "split", "show")
	for _, want := range []string{"exclude", "203.0.113.0/24", "example.com", "example.net"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCLIStatusShowsRouting(t *testing.T) {
	cases := []struct {
		mode  string
		extra func() map[string]any
		want  []string
	}{
		{"all", func() map[string]any { return splitStatus("all", nil, nil) }, []string{"Routing: all traffic through the tunnel"}},
		{"exclude", func() map[string]any {
			return splitStatus("exclude", []string{"203.0.113.0/24"}, []string{"example.com"})
		},
			[]string{"Routing: all traffic except the list (1 subnets, 1 domains)"}},
		{"only", func() map[string]any {
			m := splitStatus("only", nil, []string{"example.com"})
			m["splitStatus"] = map[string]any{"mode": "only", "resolved": 3, "resolveError": "example.org: servfail"}
			return m
		}, []string{"Routing: only the list through the tunnel (0 subnets, 1 domains)", "3 addresses resolved", "servfail"}},
	}
	for _, c := range cases {
		sock, s := serve(t)
		s.extra = c.extra()
		_, out, _ := do(t, sock, "", "status")
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output lacks %q:\n%s", c.mode, w, out)
			}
		}
	}
}

func TestCLIDaemonErrorForModeChangeIsPrinted(t *testing.T) {
	sock, s := serve(t)
	s.extra = splitStatus("all", nil, nil)
	s.setErr = "disconnect first to change the routing mode"
	code, _, errs := do(t, sock, "", "split", "mode", "only")
	if code != 1 || !strings.Contains(errs, "disconnect first") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}
