# SSH to the server through Yggdrasil (auto fallback)

## Problem
Every server function of the app (peer catalog, devices, wrapper, server panel, speed script) runs a script over SSH
(`ServerCall` → Go `setup.go` `ssh.Dial` to the server's public IPv4). The app is excluded from the VPN
(`YggVpnService.kt:154`), so that SSH goes out directly. When the server's IPv4 is unreachable from the phone's
network but the server is reachable over Yggdrasil, these functions fail. The server already accepts
`tcp $SSH_PORT` on `ygg0` (`server.sh:135`) and the app stores `result.yggAddress`.

## Goal
`ssh.Dial` falls back to `[yggAddress]:port` through the phone's running Yggdrasil node, automatically.
No UI switch. No change on the wire for existing traffic.

## Behaviour
1. Dial `host:port` directly with a short timeout (5 s; the SSH handshake timeout stays 20 s).
2. If that fails (dial error, not an auth/host-key error) and the node runs and the profile has `yggAddress`:
   dial `[yggAddress]:port` through Yggdrasil (timeout 20 s), log «Connecting through Yggdrasil».
3. If the node is not running: the original direct error is returned, with a hint that the VPN/node must be on.
4. Host key check, auth, scripts: unchanged (same server key either way).
5. `ServerCall`/Kotlin: pass `yggAddress` to Go (`SetupParams.YggAddr`, filled from `server.result.yggAddress`).

## Design
- New `go/yggdial.go`: lazily creates ONE userspace TCP stack (wireguard-go `tun/netstack`, gVisor — already in
  go.sum, no new module) per running node, and pumps packets: `netTun.Read` → `rwc.Write`, `rwc.Read` → `netTun.Write`.
  `Dial(ctx, "tcp", "[addr]:port")` is used as `ssh.NewClientConn` over the returned `net.Conn`.
- The node's single reader (`readFromYgg` in full-tunnel mode / the TUN reader in Yggdrasil-only mode) owns
  `rwc.Read`, so the stack must be fed from there: a tap `yggStack.tap(p []byte) bool` like `tapUDP`, consuming
  TCP packets addressed to the stack's local address.
- Local address of the stack: an address from the node's own /64 subnet (`300::/7` side, derived from the node's key),
  not the node's main address, so replies are unambiguously ours and never collide with other apps' TCP on the
  node address. **Risk (spike first):** ipv6rwc must accept that source; if it does not, fall back to the main
  address and demux by a reserved ephemeral port range.
- Stack is torn down with the node (Stop), and rebuilt on the next dial.
- Netstack costs ~2–4 MB in `libygg.so`; it is created only on first fallback (no idle CPU/RAM).

## Compatibility
Packets are ordinary IPv6/TCP to the server's Yggdrasil address; the existing path (WireGuard, lanes, peers, speed
protocol v2) is untouched. Neutral wording: no mention of blocking/circumvention in code, comments, notes.

## Tests
- Unit: dial fallback decision (direct fails → ygg path; auth error → no fallback; node off → original error + hint).
- Integration (in-process): two Yggdrasil cores linked locally, an `ssh` server (golang.org/x/crypto/ssh) listening
  on the second core's stack, client dials it through the first core's stack; run a command.
- Existing `go test ./...`, packet equivalence tests stay green; Kotlin unit tests green.

## Out of scope
UI toggle, SSH via lanes, using the stack for anything but SSH, the server scripts.

## Release
0.40, bilingual notes, only after your OK; no install on the phone without you.
