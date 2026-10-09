package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeServer accepts one client key, answers any exec with a canned script output
// and records the command it was asked to run.
func fakeServer(t *testing.T, client ssh.PublicKey, output string) (port int, hostFP string, cmds chan string) {
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	hs, _ := ssh.NewSignerFromKey(hk)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(client.Marshal()) {
			return nil, nil
		}
		return nil, io.EOF
	}}
	cfg.AddHostKey(hs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	cmds = make(chan string, 4)
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, creqs, _ := nch.Accept()
					go func() {
						for r := range creqs {
							if r.Type != "exec" {
								r.Reply(false, nil)
								continue
							}
							n := binary.BigEndian.Uint32(r.Payload)
							r.Reply(true, nil)
							script, _ := io.ReadAll(ch)
							cmds <- string(r.Payload[4:4+n]) + "\n" + string(script)
							if !strings.Contains(string(script), "YGGTUNNEL_RESULT") {
								io.WriteString(ch.Stderr(), "no script on stdin")
							}
							io.WriteString(ch, output)
							ch.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
							ch.Close()
						}
					}()
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, fingerprint(hs.PublicKey()), cmds
}

func clientKey(t *testing.T) (string, ssh.PublicKey) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	b, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	sp, _ := ssh.NewPublicKey(pub)
	return string(pem.EncodeToMemory(b)), sp
}

func waitSetup(t *testing.T) setupState {
	for i := 0; i < 100; i++ {
		var st setupState
		json.Unmarshal([]byte(setup.Status()), &st)
		if !st.Running {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("setup did not finish")
	return setupState{}
}

// after a change the server's description is rewritten in a second session (store.sh, ACTION=manifest)
func manifestFollows(t *testing.T, cmds chan string) {
	t.Helper()
	select {
	case cmd := <-cmds:
		if !strings.Contains(cmd, "export ACTION='manifest'") || !strings.Contains(cmd, "YggTunnel server store") {
			t.Fatalf("bad manifest command: %.200q", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no manifest update after the change")
	}
}

func TestSetup(t *testing.T) {
	key, pub := clientKey(t)
	port, fp, cmds := fakeServer(t, pub, "== Installing\nYGGTUNNEL_RESULT {\"yggAddress\":\"200::1\",\"wgPort\":51820}\n")
	p := SetupParams{Host: "127.0.0.1", Port: port, User: "root", Key: key, ClientPub: "abc=",
		WgPort: 51820, YggPort: 21443, Peers: []string{"tls://a:1", "quic://b:2"}}
	b, _ := json.Marshal(p)
	if err := setup.Start(string(b)); err != nil {
		t.Fatal(err)
	}
	st := waitSetup(t)
	if st.Error != "" || st.HostKey != fp || !strings.Contains(string(st.Result), "200::1") || !strings.Contains(st.Log, "Installing") {
		t.Fatalf("bad state %+v", st)
	}
	cmd := <-cmds
	if !strings.HasPrefix(cmd, "bash -s\n") || !strings.Contains(cmd, "export CLIENT_PUB='abc='\n") ||
		!strings.Contains(cmd, "export YGG_PEERS='tls://a:1 quic://b:2'\n") || !strings.Contains(cmd, "Configuring WireGuard") {
		t.Fatalf("bad command %q", cmd)
	}
	manifestFollows(t, cmds)

	// devices mode: the other script, extra variables quoted
	p.Mode, p.Env = "devices", map[string]string{"ACTION": "rename", "NAME_B64": "0J/RgNC40LLQtdGC"}
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	waitSetup(t)
	if cmd := <-cmds; !strings.HasPrefix(cmd, "bash -s\n") || !strings.Contains(cmd, "export ACTION='rename'\n") ||
		!strings.Contains(cmd, "export NAME_B64='0J/RgNC40LLQtdGC'\n") || !strings.Contains(cmd, "device admin") {
		t.Fatalf("bad devices command %q", cmd)
	}
	manifestFollows(t, cmds)
	// wrapper mode: its own script
	p.Mode, p.Env = "wrapper", map[string]string{"ACTION": "setup", "WRAP_TELEMT": "1"}
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	waitSetup(t)
	if cmd := <-cmds; !strings.Contains(cmd, "export WRAP_TELEMT='1'") || !strings.Contains(cmd, "the wss wrapper") {
		t.Fatalf("bad wrapper command: %.200q", cmd)
	}
	manifestFollows(t, cmds)
	// status mode: the read-only server panel script
	p.Mode, p.Env = "status", map[string]string{"ACTION": "status"}
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	waitSetup(t)
	if cmd := <-cmds; !strings.Contains(cmd, "server panel") || !strings.Contains(cmd, "export ACTION='status'") {
		t.Fatalf("bad status command: %.200q", cmd)
	}
	p.Mode = "devices"
	p.Env = map[string]string{"A; rm -rf /": "x"}
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	if st := waitSetup(t); !strings.Contains(st.Error, "bad variable name") {
		t.Fatalf("expected bad name error, got %+v", st)
	}
	p.Mode, p.Env = "", nil

	// Known host key that does not match → refuse.
	p.HostKey = "SHA256:other"
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	if st := waitSetup(t); !strings.Contains(st.Error, "server key changed") {
		t.Fatalf("expected host key error, got %+v", st)
	}

	// Non-root user → sudo.
	p.HostKey, p.User = fp, "admin"
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	waitSetup(t)
	if cmd := <-cmds; !strings.HasPrefix(cmd, "sudo -n bash -s\n") {
		t.Fatalf("expected sudo, got %q", cmd)
	}
}

func TestWgKeyPair(t *testing.T) {
	s, err := WgKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var k map[string]string
	json.Unmarshal([]byte(s), &k)
	if len(k["private"]) != 44 || len(k["public"]) != 44 || k["private"] == k["public"] {
		t.Fatalf("bad keys %v", k)
	}
}

func TestQR(t *testing.T) {
	s, err := QRMatrix("yggtunnel://import#" + strings.Repeat("A", 900))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Size int
		Rows []string
	}
	json.Unmarshal([]byte(s), &m)
	if m.Size < 21 || len(m.Rows) != m.Size || len(m.Rows[0]) != m.Size || !strings.HasPrefix(m.Rows[0], "1111111") {
		t.Fatalf("bad matrix size %d", m.Size)
	}
}
