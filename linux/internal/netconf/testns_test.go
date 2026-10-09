package netconf

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const inNSEnv = "YGGTUNNEL_TEST_INNS"

// inNetns re-runs the calling test inside a fresh user+network namespace (`unshare -rn`), where the
// test has CAP_NET_ADMIN and cannot touch the host network. It returns true in the child (run the
// body) and false in the parent (the child already ran; the caller must return).
func inNetns(t *testing.T) bool {
	t.Helper()
	if os.Getenv(inNSEnv) == "1" {
		return true
	}
	if err := exec.Command("unshare", "-rn", "true").Run(); err != nil {
		t.Skip("cannot create user+network namespaces here: " + err.Error())
	}
	cmd := exec.Command("unshare", "-rn", os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	cmd.Env = append(os.Environ(), inNSEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in namespace: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--- SKIP") {
		t.Skip(string(out))
	}
	return false
}

// snapshot is a text image of everything netconf changes.
func snapshot(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	for _, args := range [][]string{
		{"ip", "-4", "route", "show", "table", "all"}, {"ip", "-6", "route", "show", "table", "all"},
		{"ip", "-4", "rule", "show"}, {"ip", "-6", "rule", "show"},
		{"ip", "addr", "show"}, {"nft", "list", "ruleset"},
	} {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		sb.WriteString(strings.Join(args, " ") + "\n" + string(out) + "\n")
	}
	return sb.String()
}

// ownCgroup is the absolute cgroup v2 directory of this process.
func ownCgroup(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Skip("no /proc/self/cgroup")
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "0::") {
			p := "/sys/fs/cgroup" + strings.TrimPrefix(l, "0::")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	t.Skip("cgroup v2 is not available")
	return ""
}
