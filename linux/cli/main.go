// Command yggtunnelctl controls the YggTunnel daemon.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"

	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
)

const usage = `usage: yggtunnelctl [--socket PATH] <command>

  import <file|->        save a yggtunnel://import#… profile read from a file or stdin
                         (not from an argument: the link holds a private key and ps would show it)
  up                     connect
  down                   disconnect
  status [--json]        show the state
  log                    show the node log
  killswitch on|off      drop traffic that bypasses the tunnel while connected
  lan on|off             with the kill switch: keep (on) or drop (off) local-network traffic
  split mode all|exclude|only   which destinations use the tunnel (change it while disconnected)
  split add <subnet|domain>     add to the list (a bare address is a /32 or /128 subnet)
  split remove <subnet|domain>  remove from the list
  split show                    show the mode and the lists
  panic                  remove every route, rule and DNS setting the daemon added
  version                show the daemon version`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("yggtunnelctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	def := os.Getenv("YGGTUNNEL_SOCKET")
	if def == "" {
		def = "/run/yggtunnel.sock"
	}
	sock := fs.String("socket", def, "")
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd, rest := fs.Arg(0), fs.Args()[1:]
	switch cmd {
	case "import", "up", "down", "status", "log", "panic", "version", "killswitch", "lan", "split":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s\n", cmd, usage)
		return 2
	}
	var callArgs any
	if cmd == "killswitch" || cmd == "lan" {
		if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		field := "killSwitch"
		if cmd == "lan" {
			field = "allowLan"
		}
		callArgs = map[string]bool{field: rest[0] == "on"}
		cmd = "set"
	}
	if cmd == "split" && !validSplitArgs(rest) {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if cmd == "import" {
		if len(rest) != 1 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		link, err := readLink(rest[0], stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		callArgs = map[string]string{"link": link}
	}
	c, err := ipc.Dial(*sock)
	if err != nil {
		fmt.Fprintf(stderr, "cannot reach yggtunneld at %s: %v\n(is the service running? systemctl start yggtunneld)\n", *sock, err)
		return 1
	}
	defer c.Close()
	if cmd == "split" {
		return runSplit(c, rest, stdout, stderr)
	}
	var raw json.RawMessage
	if err := c.Call(cmd, callArgs, &raw); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	switch cmd {
	case "status":
		if len(rest) > 0 && rest[0] == "--json" {
			fmt.Fprintln(stdout, string(raw))
			return 0
		}
		printStatus(stdout, raw)
	case "log", "version":
		var s string
		_ = json.Unmarshal(raw, &s)
		fmt.Fprintln(stdout, s)
	}
	return 0
}

// readLink takes the profile from stdin ("-") or from a file. A link is never accepted as an
// argument: it holds a private key and every local user could read it in the process list.
func readLink(arg string, stdin io.Reader) (string, error) {
	switch {
	case arg == "-":
		b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		return string(b), err
	case strings.Contains(arg, "yggtunnel://"):
		return "", fmt.Errorf("pass the profile through a file or stdin (yggtunnelctl import -), not as an argument: the link holds a private key and ps would show it")
	}
	b, err := os.ReadFile(arg)
	if err != nil {
		return "", fmt.Errorf("cannot read %q: %w", arg, err)
	}
	return string(b), nil
}

func printStatus(w io.Writer, raw json.RawMessage) {
	var s struct {
		State   string `json:"state"`
		Error   string `json:"error"`
		Profile struct {
			Name      string `json:"name"`
			ServerYgg string `json:"serverYgg"`
		} `json:"profile"`
		Settings struct {
			KillSwitch bool `json:"killSwitch"`
			AllowLAN   bool `json:"allowLan"`
			Split      struct {
				Mode    string   `json:"mode"`
				Subnets []string `json:"subnets"`
				Domains []string `json:"domains"`
			} `json:"split"`
		} `json:"settings"`
		KillSwitchActive bool `json:"killSwitchActive"`
		SplitStatus      struct {
			Mode         string `json:"mode"`
			Resolved     int    `json:"resolved"`
			ResolveError string `json:"resolveError"`
		} `json:"splitStatus"`
	}
	_ = json.Unmarshal(raw, &s)
	fmt.Fprintf(w, "State:   %s\n", s.State)
	if s.Profile.ServerYgg != "" {
		fmt.Fprintf(w, "Profile: %s (%s)\n", s.Profile.Name, s.Profile.ServerYgg)
	} else {
		fmt.Fprintln(w, "Profile: none (yggtunnelctl import <link>)")
	}
	switch {
	case s.KillSwitchActive:
		fmt.Fprintln(w, "Kill switch: active")
	case s.Settings.KillSwitch:
		fmt.Fprintln(w, "Kill switch: armed (starts with the next connection)")
	default:
		fmt.Fprintln(w, "Kill switch: off")
	}
	if s.Settings.KillSwitch {
		if s.Settings.AllowLAN {
			fmt.Fprintln(w, "Local network: allowed")
		} else {
			fmt.Fprintln(w, "Local network: dropped")
		}
	}
	sp := s.Settings.Split
	lists := fmt.Sprintf("(%d subnets, %d domains)", len(sp.Subnets), len(sp.Domains))
	switch sp.Mode {
	case "exclude":
		fmt.Fprintf(w, "Routing: all traffic except the list %s\n", lists)
	case "only":
		fmt.Fprintf(w, "Routing: only the list through the tunnel %s\n", lists)
	default:
		fmt.Fprintln(w, "Routing: all traffic through the tunnel")
	}
	if s.SplitStatus.Mode != "" && s.SplitStatus.Mode != "all" && len(sp.Domains) > 0 {
		fmt.Fprintf(w, "Names: %d addresses resolved\n", s.SplitStatus.Resolved)
	}
	if s.SplitStatus.ResolveError != "" {
		fmt.Fprintf(w, "Name resolution problem: %s\n", s.SplitStatus.ResolveError)
	}
	if s.Error != "" {
		fmt.Fprintf(w, "Last error: %s\n", s.Error)
	}
}

// validSplitArgs checks the arguments of `split` before anything is sent.
func validSplitArgs(rest []string) bool {
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case "mode":
		return len(rest) == 2 && (rest[1] == "all" || rest[1] == "exclude" || rest[1] == "only")
	case "add", "remove":
		return len(rest) == 2 && strings.TrimSpace(rest[1]) != ""
	case "show":
		return len(rest) == 1
	}
	return false
}

type splitLists struct {
	Mode    string   `json:"mode"`
	Subnets []string `json:"subnets"`
	Domains []string `json:"domains"`
}

// isSubnet tells a subnet or address from a domain name; the daemon validates the entry either way.
func isSubnet(entry string) bool {
	if strings.Contains(entry, "/") {
		return true
	}
	_, err := netip.ParseAddr(entry)
	return err == nil
}

// canon is the form in which the daemon stores an entry, so that "192.0.2.7" finds "192.0.2.7/32".
func canon(entry string, subnet bool) string {
	entry = strings.TrimSpace(entry)
	if !subnet {
		return strings.ToLower(strings.TrimSuffix(entry, "."))
	}
	if p, err := netip.ParsePrefix(entry); err == nil {
		return p.Masked().String()
	}
	if a, err := netip.ParseAddr(entry); err == nil {
		return netip.PrefixFrom(a, a.BitLen()).String()
	}
	return entry
}

func indexOf(list []string, entry string, subnet bool) int {
	want := canon(entry, subnet)
	for i, e := range list {
		if canon(e, subnet) == want {
			return i
		}
	}
	return -1
}

// runSplit implements `split mode|add|remove|show`: read the lists from the daemon, change them, send
// the whole object back in one `set`.
func runSplit(c *ipc.Client, rest []string, stdout, stderr io.Writer) int {
	var st struct {
		Settings struct {
			Split splitLists `json:"split"`
		} `json:"settings"`
	}
	if err := c.Call("status", nil, &st); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	cur := st.Settings.Split
	if cur.Mode == "" {
		cur.Mode = "all"
	}
	send := func(next splitLists) int {
		if err := c.Call("set", map[string]any{"split": next}, nil); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	switch rest[0] {
	case "show":
		fmt.Fprintf(stdout, "Mode: %s\n", cur.Mode)
		fmt.Fprintf(stdout, "Subnets (%d):\n", len(cur.Subnets))
		for _, s := range cur.Subnets {
			fmt.Fprintf(stdout, "  %s\n", s)
		}
		fmt.Fprintf(stdout, "Domains (%d):\n", len(cur.Domains))
		for _, d := range cur.Domains {
			fmt.Fprintf(stdout, "  %s\n", d)
		}
		return 0
	case "mode":
		cur.Mode = rest[1]
		return send(cur)
	}
	entry := strings.TrimSpace(rest[1])
	subnet := isSubnet(entry)
	list := &cur.Domains
	if subnet {
		list = &cur.Subnets
	}
	i := indexOf(*list, entry, subnet)
	if rest[0] == "add" {
		if i >= 0 {
			fmt.Fprintf(stdout, "%s is already in the list\n", entry)
			return 0
		}
		*list = append(*list, entry)
	} else {
		if i < 0 {
			fmt.Fprintf(stdout, "%s is not in the list\n", entry)
			return 0
		}
		*list = append((*list)[:i], (*list)[i+1:]...)
	}
	return send(cur)
}
