// Command yggtunneld is the root daemon of YggTunnel for Linux.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Xtratter/yggtunnel/linux/internal/auth"
	"github.com/Xtratter/yggtunnel/linux/internal/daemon"
	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
	"github.com/Xtratter/yggtunnel/linux/internal/version"
)

func main() {
	stateDir := flag.String("state-dir", "/var/lib/yggtunnel", "state directory")
	socket := flag.String("socket", "/run/yggtunnel.sock", "unix socket path")
	group := flag.String("group", "yggtunnel", "group that may use the socket")
	dry := flag.Bool("dry-run", false, "log network changes instead of applying them")
	noPolkit := flag.Bool("no-polkit", false, "skip polkit (development only: needs --dry-run or YGGTUNNEL_DEV=1)")
	recoverOnly := flag.Bool("recover-only", false, "undo network changes left by an earlier run and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	if os.Geteuid() != 0 && !*dry {
		log.Fatal("yggtunneld must run as root (use --dry-run to try it without)")
	}
	if *noPolkit && !*dry && os.Getenv("YGGTUNNEL_DEV") != "1" {
		log.Fatal("--no-polkit is for development: add --dry-run or set YGGTUNNEL_DEV=1")
	}

	st, err := store.Open(*stateDir)
	if err != nil {
		log.Fatal(err)
	}
	var srv *ipc.Server
	emit := func(e ipc.Event) {
		if srv != nil {
			srv.Broadcast(e)
		}
	}
	var net daemon.Net = daemon.RealNet{}
	if *dry {
		net = daemon.DryNet{}
	}
	d := daemon.New(st, daemon.RealCore{}, net, emit)
	d.CgroupPath = ownCgroup()
	if !*noPolkit {
		if d.Auth, err = auth.NewPolkit(); err != nil {
			log.Fatalf("polkit is required: %v", err)
		}
	}
	if err := d.Recover(); err != nil {
		log.Printf("recovering an earlier run: %v", err)
	}
	if *recoverOnly {
		return
	}
	if d.CgroupPath == "" && !*dry {
		log.Fatal("cannot find the daemon's cgroup v2 directory (is cgroup v2 mounted?)")
	}
	if srv, err = ipc.Listen(*socket, *group, d); err != nil {
		log.Fatal(err)
	}
	log.Printf("yggtunneld %s listening on %s", version.String(), *socket)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Print("stopping: undoing network changes")
	if _, err := d.Handle(context.Background(), ipc.Peer{}, ipc.Request{Cmd: "down"}); err != nil {
		log.Printf("down: %v", err)
	}
	srv.Close()
}

// ownCgroup is the absolute cgroup v2 directory of this process, or "".
func ownCgroup() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if p, ok := strings.CutPrefix(l, "0::"); ok {
			dir := "/sys/fs/cgroup" + p
			if _, err := os.Stat(dir); err == nil {
				return dir
			}
		}
	}
	return ""
}
