package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// pwServer: logs in by password "pw" or by a key from its own authorized_keys (which a client may append
// to with `… cat >> ~/.ssh/authorized_keys`); `sudo -n true` fails (sudo wants a password); any other exec
// is a script: recorded with its stdin and answered with [output].
func pwServer(t *testing.T, output string) (port int, cmds chan string, keyLogins *int) {
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	hs, _ := ssh.NewSignerFromKey(hk)
	var mu sync.Mutex
	authorized := map[string]bool{}
	logins := 0
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == "pw" {
				return nil, nil
			}
			return nil, errors.New("no")
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			mu.Lock()
			defer mu.Unlock()
			if authorized[string(k.Marshal())] {
				logins++
				return nil, nil
			}
			return nil, errors.New("no")
		},
	}
	cfg.AddHostKey(hs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	cmds = make(chan string, 8)
	exit := func(ch ssh.Channel, code uint32) {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, code)
		ch.SendRequest("exit-status", false, b)
		ch.Close()
	}
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
							cmd := string(r.Payload[4 : 4+n])
							r.Reply(true, nil)
							switch {
							case cmd == "sudo -n true":
								exit(ch, 1)
							case strings.Contains(cmd, "authorized_keys"):
								line, _ := io.ReadAll(ch)
								if pk, _, _, _, err := ssh.ParseAuthorizedKey(line); err == nil {
									mu.Lock()
									authorized[string(pk.Marshal())] = true
									mu.Unlock()
								}
								exit(ch, 0)
							default:
								in, _ := io.ReadAll(ch)
								cmds <- cmd + "\n" + string(in)
								io.WriteString(ch, output)
								exit(ch, 0)
							}
						}
					}()
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, cmds, &logins
}

func TestSetupByPassword(t *testing.T) {
	port, cmds, keyLogins := pwServer(t, "YGGTUNNEL_RESULT {\"yggAddress\":\"200::1\",\"wgPort\":51820}\n")
	p := SetupParams{Host: "127.0.0.1", Port: port, User: "root", Password: "pw", ClientPub: "abc=", WgPort: 51820, YggPort: 21443}
	b, _ := json.Marshal(p)
	if err := setup.Start(string(b)); err != nil {
		t.Fatal(err)
	}
	st := waitSetup(t)
	if st.Error != "" || !strings.Contains(string(st.Result), "200::1") {
		t.Fatalf("setup by password: %+v", st)
	}
	if !strings.Contains(st.SSHKey, "OPENSSH PRIVATE KEY") || *keyLogins == 0 {
		t.Fatalf("no checked key: key %d bytes, key logins %d", len(st.SSHKey), *keyLogins)
	}
	if cmd := <-cmds; !strings.HasPrefix(cmd, "bash -s\n") || strings.Contains(cmd, "pw\n") {
		t.Fatalf("root: %.80q", cmd)
	}
	<-cmds // manifest

	// from then on the key alone is enough
	p.Password, p.Key, p.HostKey = "", st.SSHKey, st.HostKey
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	if st2 := waitSetup(t); st2.Error != "" || st2.SSHKey != "" {
		t.Fatalf("by the new key: %+v", st2)
	}
	<-cmds
	<-cmds

	// a user with sudo that wants a password: the password goes first on stdin, for sudo -S
	p = SetupParams{Host: "127.0.0.1", Port: port, User: "admin", Password: "pw", Mode: "status", Env: map[string]string{"ACTION": "status"}}
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	if st3 := waitSetup(t); st3.Error != "" {
		t.Fatalf("sudo: %+v", st3)
	}
	if cmd := <-cmds; !strings.HasPrefix(cmd, "sudo -S -k -p '' bash -s\npw\nexport ") {
		t.Fatalf("sudo -S: %.80q", cmd)
	}

	// a wrong password: a clear error
	p.Password = "nope"
	b, _ = json.Marshal(p)
	setup.Start(string(b))
	if st4 := waitSetup(t); !strings.Contains(st4.Error, "refused the password") {
		t.Fatalf("wrong password: %+v", st4)
	}
}
