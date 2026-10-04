package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// store.sh ACTION=desec against a fake deSEC API: a new name.dedyn.io is registered, a name under an owned
// domain only gets a record, the A record lands with the domain's minimum TTL, a bad token is reported.
func TestStoreDesec(t *testing.T) {
	var mu sync.Mutex
	owned := []string{"myhome.dedyn.io"}
	var created []string
	rrsets := map[string]string{} // domain → last PATCH body
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Token good" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		p := strings.TrimPrefix(r.URL.Path, "/api/v1")
		switch {
		case r.Method == "GET" && p == "/domains/":
			var out []map[string]string
			for _, d := range owned {
				out = append(out, map[string]string{"name": d})
			}
			json.NewEncoder(w).Encode(out)
		case r.Method == "POST" && p == "/domains/":
			var d struct{ Name string }
			json.Unmarshal(body, &d)
			if d.Name == "taken.dedyn.io" {
				w.WriteHeader(400)
				w.Write([]byte(`{"name":["This domain name is unavailable."]}`))
				return
			}
			owned = append(owned, d.Name)
			created = append(created, d.Name)
			w.WriteHeader(201)
			w.Write([]byte(`{}`))
		case r.Method == "GET" && strings.HasPrefix(p, "/domains/") && strings.Count(p, "/") == 3:
			w.Write([]byte(`{"minimum_ttl": 60}`))
		case r.Method == "PATCH" && strings.HasSuffix(p, "/rrsets/"):
			rrsets[strings.Split(p, "/")[2]] = string(body)
			w.Write(body)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	run := func(env ...string) (string, error) {
		cmd := exec.Command("bash", "store.sh")
		cmd.Env = append(os.Environ(), append([]string{"STORE_DIR=" + t.TempDir(), "ACTION=desec", "DESEC_API=" + ts.URL + "/api/v1"}, env...)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("DESEC_TOKEN=good", "DESEC_OP=list"); err != nil || !strings.Contains(out, `"domains":["myhome.dedyn.io"]`) {
		t.Fatalf("list: %v %s", err, out)
	}
	out, err := run("DESEC_TOKEN=good", "DESEC_OP=claim", "DESEC_NAME=MyVPN.dedyn.io", "DESEC_IP=203.0.113.7")
	if err != nil || !strings.Contains(out, `"domain":"myvpn.dedyn.io"`) || !strings.Contains(rrsets["myvpn.dedyn.io"], `"subname": ""`) ||
		!strings.Contains(rrsets["myvpn.dedyn.io"], "203.0.113.7") || !strings.Contains(rrsets["myvpn.dedyn.io"], `"ttl": 60`) {
		t.Fatalf("new name: %v %s %v", err, out, rrsets)
	}
	if len(created) != 1 || created[0] != "myvpn.dedyn.io" {
		t.Fatalf("created %v", created)
	}
	out, err = run("DESEC_TOKEN=good", "DESEC_OP=claim", "DESEC_NAME=vpn.myhome.dedyn.io", "DESEC_IP=203.0.113.8")
	if err != nil || !strings.Contains(rrsets["myhome.dedyn.io"], `"subname": "vpn"`) || len(created) != 1 {
		t.Fatalf("subname: %v %s %v %v", err, out, rrsets, created)
	}
	if out, err = run("DESEC_TOKEN=good", "DESEC_OP=claim", "DESEC_NAME=vpn.example.org", "DESEC_IP=203.0.113.8"); err == nil || !strings.Contains(out, "myvpn.dedyn.io") {
		t.Fatalf("foreign domain accepted: %s", out)
	}
	if out, err = run("DESEC_TOKEN=good", "DESEC_OP=claim", "DESEC_NAME=taken.dedyn.io", "DESEC_IP=203.0.113.8"); err == nil || !strings.Contains(out, "unavailable") {
		t.Fatalf("taken name: %s", out)
	}
	if out, err = run("DESEC_TOKEN=bad", "DESEC_OP=list"); err == nil || !strings.Contains(out, "token is not valid") {
		t.Fatalf("bad token: %s", out)
	}
}
