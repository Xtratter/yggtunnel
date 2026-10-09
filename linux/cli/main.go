// Command yggtunnelctl controls the YggTunnel daemon.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	case "import", "up", "down", "status", "log", "panic", "version", "killswitch", "lan":
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
		} `json:"settings"`
		KillSwitchActive bool `json:"killSwitchActive"`
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
	if s.Error != "" {
		fmt.Fprintf(w, "Last error: %s\n", s.Error)
	}
}
