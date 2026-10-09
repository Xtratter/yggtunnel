package core

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

// Upload diagnostics: the kernel's own view of every TCP socket of this process (Yggdrasil's tls / tcp /
// ws links) — congestion window, slow-start threshold, round trip, losses, delivery rate — read with
// getsockopt(TCP_INFO) on the descriptors in /proc/self/fd (yggdrasil-go gives no hook into its sockets).
// Sampled once a second together with WireGuard's counters into a text log the app sends to the server.

type tcpSock struct {
	fd     int
	local  uint16 // the local port: several lanes share the same remote address
	remote netip.AddrPort
	cc     string
	info   *unix.TCPInfo
}

// bindDrops counts WireGuard datagrams from Yggdrasil dropped because WireGuard lagged (yggBind.deliver).
var bindDrops atomic.Uint64

// includeLoopback: tests run their links over 127.0.0.1.
var includeLoopback = false

// ownTCPSockets: the connected TCP sockets of this process, loopback excluded.
func ownTCPSockets() []tcpSock {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return nil
	}
	var out []tcpSock
	for _, e := range ents {
		fd, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if l, err := os.Readlink("/proc/self/fd/" + e.Name()); err != nil || !strings.HasPrefix(l, "socket:") {
			continue
		}
		if t, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE); err != nil || t != unix.SOCK_STREAM {
			continue
		}
		sa, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}
		var ap netip.AddrPort
		switch a := sa.(type) {
		case *unix.SockaddrInet4:
			ap = netip.AddrPortFrom(netip.AddrFrom4(a.Addr), uint16(a.Port))
		case *unix.SockaddrInet6:
			ap = netip.AddrPortFrom(netip.AddrFrom16(a.Addr).Unmap(), uint16(a.Port))
		default:
			continue
		}
		if ap.Addr().IsLoopback() && !includeLoopback {
			continue
		}
		info, err := unix.GetsockoptTCPInfo(fd, unix.IPPROTO_TCP, unix.TCP_INFO)
		if err != nil {
			continue
		}
		cc, _ := unix.GetsockoptString(fd, unix.IPPROTO_TCP, unix.TCP_CONGESTION)
		var local uint16
		switch a := func() unix.Sockaddr { sa, _ := unix.Getsockname(fd); return sa }().(type) {
		case *unix.SockaddrInet4:
			local = uint16(a.Port)
		case *unix.SockaddrInet6:
			local = uint16(a.Port)
		}
		out = append(out, tcpSock{fd: fd, local: local, remote: ap, cc: strings.TrimRight(cc, "\x00"), info: info})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].info.Bytes_sent > out[j].info.Bytes_sent })
	return out
}

// ---- the send queue of the link sockets ----
//
// A link socket's kernel send buffer grows to megabytes: on a lossy mobile uplink 03.10 it held up to 1.9 MB
// not yet sent — about 4 s of queue at 4 Mbit/s, so everything inside the tunnel saw seconds of delay and its
// TCP collapsed. TCP_NOTSENT_LOWAT keeps at most this much unsent in the kernel: Yggdrasil's writer then waits,
// and its own small queue drops packets — a quick signal for the connections inside the tunnel.

// LinkLowatDefault — the unsent-bytes limit on link sockets; 0 — the system default (no limit).
const LinkLowatDefault = 128 << 10

var linkLowat atomic.Int64

var lowatOnce sync.Once

// startLowat applies the limit every 2 s to every link socket (yggdrasil-go has no hook for its sockets,
// so new links are caught by polling /proc/self/fd).
func startLowat() {
	lowatOnce.Do(func() {
		linkLowat.Store(LinkLowatDefault)
		go func() {
			for {
				applyLowat()
				time.Sleep(2 * time.Second)
			}
		}()
	})
}

func applyLowat() {
	want := int(linkLowat.Load())
	for _, s := range ownTCPSockets() {
		if cur, err := unix.GetsockoptInt(s.fd, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT); err == nil && cur != want {
			_ = unix.SetsockoptInt(s.fd, unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, want)
		}
	}
}

// SetLinkLowat changes the limit (bytes, 0 — none) and applies it at once.
func SetLinkLowat(bytes int) {
	startLowat()
	linkLowat.Store(int64(max(bytes, 0)))
	applyLowat()
}

func LinkLowat() int { return int(linkLowat.Load()) }

// DiagSetCC switches the congestion control of the live link sockets ("westwood", "reno", …); a reconnect
// gets the system default again. Returns how many sockets took it.
func DiagSetCC(name string) (int, error) {
	n := 0
	var last error
	for _, s := range ownTCPSockets() {
		if err := unix.SetsockoptString(s.fd, unix.IPPROTO_TCP, unix.TCP_CONGESTION, name); err != nil {
			last = err
			continue
		}
		n++
	}
	if n == 0 && last != nil {
		return 0, last
	}
	return n, nil
}

type tcpDiag struct {
	mu      sync.Mutex
	running bool
	stop    chan struct{}
	lines   []string
	until   time.Time
}

var diag tcpDiag

var caStates = []string{"open", "disorder", "cwr", "recovery", "loss"}

func mbit(bytes uint64, secs float64) string {
	return strconv.FormatFloat(float64(bytes)*8/1e6/secs, 'f', 1, 64)
}

// DiagStart samples for [seconds] in the background; the text so far comes from DiagText.
func DiagStart(seconds int) error {
	diag.mu.Lock()
	defer diag.mu.Unlock()
	if diag.running {
		return errors.New("already running")
	}
	if seconds <= 0 || seconds > 600 {
		seconds = 120
	}
	stop := make(chan struct{})
	diag.running, diag.stop, diag.until = true, stop, time.Now().Add(time.Duration(seconds)*time.Second)
	diag.lines = []string{fmt.Sprintf("# YggTunnel upload diagnostics %s, %d s, unsent limit %d KB (0 — none)", time.Now().Format("2006-01-02 15:04:05 -0700"), seconds, LinkLowat()>>10),
		"# per socket: remote cc ca-state cwnd/ssthresh (segments) mss rtt/var min (ms) sent Mbit/s, retrans+ lost, delivery & pacing Mbit/s, unsent KB, limited by rwnd/sndbuf ms",
		"# wg: WireGuard tx/rx Mbit/s, drops: WireGuard datagrams dropped from Yggdrasil"}
	go diagLoop(stop, seconds)
	return nil
}

func DiagStop() {
	diag.mu.Lock()
	defer diag.mu.Unlock()
	if diag.running {
		close(diag.stop)
		diag.running = false
	}
}

// DiagStatus: {"running":…, "left": seconds, "lines": n}.
func DiagStatus() string {
	diag.mu.Lock()
	defer diag.mu.Unlock()
	left := 0
	if diag.running {
		left = int(time.Until(diag.until).Seconds() + 0.5)
	}
	return fmt.Sprintf(`{"running":%t,"left":%d,"lines":%d}`, diag.running, max(left, 0), len(diag.lines))
}

func DiagText() string {
	diag.mu.Lock()
	defer diag.mu.Unlock()
	return strings.Join(diag.lines, "\n") + "\n"
}

func diagAdd(s string) {
	diag.mu.Lock()
	diag.lines = append(diag.lines, s)
	diag.mu.Unlock()
}

func wgCounters() (tx, rx uint64, ok bool) {
	node.mu.Lock()
	st := node.tunnelStats(time.Now().Unix())
	node.mu.Unlock()
	if st == nil {
		return 0, 0, false
	}
	return st.Tx, st.Rx, true
}

func diagLoop(stop chan struct{}, seconds int) {
	defer func() {
		diag.mu.Lock()
		if diag.stop == stop {
			diag.running = false
		}
		diag.mu.Unlock()
	}()
	type prev struct{ sent, retr, rwnd, sndbuf uint64 }
	last := map[string]prev{}
	wtx, wrx, _ := wgCounters()
	drops := bindDrops.Load()
	t0 := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for i := 0; i < seconds; i++ {
		select {
		case <-stop:
			diagAdd("# stopped")
			return
		case <-tick.C:
		}
		secs := time.Since(t0).Seconds()
		t0 = time.Now()
		var b strings.Builder
		b.WriteString(time.Now().Format("15:04:05"))
		for _, s := range ownTCPSockets() {
			in := s.info
			// keyed by the local port too: lanes all go to the same server address and port, and keyed by
			// that alone their counters got mixed up (garbage «sent» in the 04.10 log)
			key := strconv.Itoa(int(s.local)) + ">" + s.remote.String()
			p, seen := last[key]
			last[key] = prev{in.Bytes_sent, uint64(in.Total_retrans), in.Rwnd_limited, in.Sndbuf_limited}
			if !seen {
				p = last[key]
			}
			ssth := "inf"
			if in.Snd_ssthresh < 0x7fff0000 {
				ssth = strconv.Itoa(int(in.Snd_ssthresh))
			}
			ca := strconv.Itoa(int(in.Ca_state))
			if int(in.Ca_state) < len(caStates) {
				ca = caStates[in.Ca_state]
			}
			fmt.Fprintf(&b, " | :%d>%s %s %s cwnd=%d/%s mss=%d rtt=%.0f/%.0f min=%.0f sent=%s retr+%d lost=%d deliv=%s pace=%s unsent=%dK lim=%d/%d",
				s.local, s.remote, s.cc, ca, in.Snd_cwnd, ssth, in.Snd_mss, float64(in.Rtt)/1000, float64(in.Rttvar)/1000, float64(in.Min_rtt)/1000,
				mbit(in.Bytes_sent-p.sent, secs), uint64(in.Total_retrans)-p.retr, in.Lost,
				mbit(in.Delivery_rate, 1), mbit(in.Pacing_rate, 1), in.Notsent_bytes/1024,
				(in.Rwnd_limited-p.rwnd)/1000, (in.Sndbuf_limited-p.sndbuf)/1000)
		}
		if tx, rx, ok := wgCounters(); ok {
			fmt.Fprintf(&b, " | wg tx=%s rx=%s", mbit(tx-wtx, secs), mbit(rx-wrx, secs))
			wtx, wrx = tx, rx
		}
		d := bindDrops.Load()
		fmt.Fprintf(&b, " drops+%d", d-drops)
		drops = d
		diagAdd(b.String())
	}
}
