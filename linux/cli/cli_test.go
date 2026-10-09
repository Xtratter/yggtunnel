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
	got   []ipc.Request
	extra map[string]any // merged into the status answer
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
			[]string{"Kill switch: active", "Local network: blocked"}, nil},
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
