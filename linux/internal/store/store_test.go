package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/profile"
	"github.com/Xtratter/yggtunnel/linux/internal/splitcfg"
)

func sample() profile.Profile {
	return profile.Profile{V: 1, Name: "example", PrivateKey: "SECRETKEY-abcd", ServerKey: "c2VydmVy",
		ServerYgg: "200:db8::1", Port: 51820, ClientIP4: "192.0.2.10"}
}

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProfileRoundTrip(t *testing.T) {
	s := open(t)
	if err := s.SaveProfile(sample()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Profile()
	if err != nil || got.PrivateKey != "SECRETKEY-abcd" || got.ServerYgg != "200:db8::1" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestProfileFileDoesNotContainPrivateKey(t *testing.T) {
	s := open(t)
	s.SaveProfile(sample())
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if strings.Contains(string(b), "SECRETKEY-abcd") {
			t.Fatalf("%s contains the private key in clear", e.Name())
		}
	}
}

func TestFilesAre0600(t *testing.T) {
	s := open(t)
	s.SaveProfile(sample())
	s.SavePrev(PrevState{Steps: []Step{{Kind: "link"}}})
	entries, _ := os.ReadDir(s.Dir)
	if len(entries) < 3 {
		t.Fatalf("expected key, profile and prev files, got %d", len(entries))
	}
	for _, e := range entries {
		fi, _ := e.Info()
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", e.Name(), fi.Mode().Perm())
		}
	}
	fi, _ := os.Stat(s.Dir)
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
}

func TestMaskedProfileHidesKey(t *testing.T) {
	s := open(t)
	s.SaveProfile(sample())
	m := s.MaskedProfile()
	if m.PrivateKey != "…abcd" || m.ServerYgg != "200:db8::1" {
		t.Fatalf("%+v", m)
	}
}

func TestMaskedProfileEmptyWhenNone(t *testing.T) {
	if m := open(t).MaskedProfile(); m.ServerYgg != "" || m.PrivateKey != "" {
		t.Fatalf("%+v", m)
	}
}

func TestPrevRoundTripAndClear(t *testing.T) {
	s := open(t)
	in := PrevState{Steps: []Step{{Kind: "addr", Args: map[string]string{"if": "yggtun0"}}}}
	if err := s.SavePrev(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Prev()
	if err != nil || !ok || len(got.Steps) != 1 || got.Steps[0].Args["if"] != "yggtun0" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if err := s.ClearPrev(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Prev(); ok {
		t.Fatal("prev still present")
	}
}

func TestPrevMissingIsNotError(t *testing.T) {
	if _, ok, err := open(t).Prev(); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestClearPrevWhenMissingIsNotError(t *testing.T) {
	if err := open(t).ClearPrev(); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptProfileReturnsError(t *testing.T) {
	s := open(t)
	s.SaveProfile(sample())
	os.WriteFile(filepath.Join(s.Dir, "profile.json"), []byte("{broken"), 0o600)
	if _, err := s.Profile(); err == nil {
		t.Fatal("expected error")
	}
}

func TestNoProfileIsError(t *testing.T) {
	if _, err := open(t).Profile(); err == nil {
		t.Fatal("expected error")
	}
}

func TestTamperedCiphertextIsError(t *testing.T) {
	s := open(t)
	s.SaveProfile(sample())
	b, _ := os.ReadFile(filepath.Join(s.Dir, "profile.json"))
	b = []byte(strings.Replace(string(b), "enc:", "enc:AAAA", 1))
	os.WriteFile(filepath.Join(s.Dir, "profile.json"), b, 0o600)
	if _, err := s.Profile(); err == nil {
		t.Fatal("expected error")
	}
}

func TestNodeConfigGeneratedOnceAndEncrypted(t *testing.T) {
	s := open(t)
	n := 0
	gen := func() (string, error) { n++; return `{"PrivateKey":"NODESECRET"}`, nil }
	a, err := s.NodeConfig(gen)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.NodeConfig(gen)
	if err != nil || a != b || n != 1 {
		t.Fatalf("a=%q b=%q gen calls=%d err=%v", a, b, n, err)
	}
	raw, _ := os.ReadFile(filepath.Join(s.Dir, "node.json"))
	if strings.Contains(string(raw), "NODESECRET") {
		t.Fatal("node config stored in clear")
	}
}

func TestSettingsDefaults(t *testing.T) {
	s := open(t).Settings()
	if s.KillSwitch || !s.AllowLAN {
		t.Fatalf("defaults %+v: want kill switch off, local network allowed", s)
	}
}

func TestSettingsPersistAndSurviveDamagedFile(t *testing.T) {
	st := open(t)
	if err := st.SaveSettings(Settings{KillSwitch: true, AllowLAN: false}); err != nil {
		t.Fatal(err)
	}
	if got := st.Settings(); !got.KillSwitch || got.AllowLAN {
		t.Fatalf("got %+v", got)
	}
	os.WriteFile(filepath.Join(st.Dir, "settings.json"), []byte("{broken"), 0o600)
	if got := st.Settings(); got.KillSwitch || !got.AllowLAN {
		t.Fatalf("a damaged file must give the defaults, got %+v", got)
	}
}

func TestSettingsSplitDefaultsToModeAll(t *testing.T) {
	s := open(t).Settings()
	if s.Split.Mode != "all" || len(s.Split.Subnets) != 0 || len(s.Split.Domains) != 0 {
		t.Fatalf("%+v", s.Split)
	}
}

func TestSettingsSplitRoundTrip(t *testing.T) {
	st := open(t)
	in := Settings{AllowLAN: true, Split: splitcfg.Config{Mode: "only", Subnets: []string{"203.0.113.0/24"}, Domains: []string{"example.com"}}}
	if err := st.SaveSettings(in); err != nil {
		t.Fatal(err)
	}
	got := st.Settings()
	if got.Split.Mode != "only" || got.Split.Subnets[0] != "203.0.113.0/24" || got.Split.Domains[0] != "example.com" {
		t.Fatalf("%+v", got.Split)
	}
}

func TestOldSettingsFileWithoutSplitLoadsAsModeAll(t *testing.T) {
	st := open(t)
	os.WriteFile(filepath.Join(st.Dir, "settings.json"), []byte(`{"killSwitch":true,"allowLan":false}`), 0o600)
	got := st.Settings()
	if !got.KillSwitch || got.AllowLAN || got.Split.Mode != "all" {
		t.Fatalf("%+v", got)
	}
}
