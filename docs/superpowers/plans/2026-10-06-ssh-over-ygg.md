# SSH over Yggdrasil (auto fallback) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (native, no subagents). Steps use `- [ ]`.

**Goal:** `ssh.Dial` falls back to `[yggAddress]:port` through the running node.
**Architecture:** userspace TCP stack (wireguard-go `tun/netstack`) on top of `ipv6rwc`, local address from the node's /64 subnet (ipv6rwc accepts subnet sources on Write and subnet destinations on Read — verified in its code), fed by a tap in the node's existing reader goroutines.
**Tech Stack:** Go, wireguard-go netstack (gVisor already in go.sum), x/crypto/ssh.
**Spec:** docs/superpowers/specs/2026-10-06-ssh-over-ygg-design.md

## Global Constraints
- Existing traffic untouched; neutral wording (no blocking/circumvention words in code, comments, notes, commits).
- Direct dial timeout 5 s only when the profile has `result.yggAddress`, else 20 s as before. Handshake/auth/host-key logic unchanged.
- No fallback on auth errors or host-key mismatch. Node off → original error + hint.
- Commit author = repo's existing (noreply); nothing pushed until the user says so.

## Review Focus
- Packets for other apps' TCP to the node address must NOT be taken by the tap (only dst == stack's local subnet address).
- Stack must be rebuilt after node restart (new rwc) and closed on Stop; no goroutine leak.
- Direct dial that hangs vs. refuses: both fall back; `unable to authenticate` and `server key changed` never do.

### Task 1: yggStack (TCP over a node) + in-process SSH test
**Files:** Create `go/yggdial.go`, `go/yggdial_test.go`
**Produces:** `newYggStack(rwc *ipv6rwc.ReadWriteCloser, local netip.Addr, mtu int) (*yggStack, error)`; `(*yggStack).tap(p []byte) bool`; `(*yggStack).DialContext(ctx, addr string) (net.Conn, error)`; `(*yggStack).Listen(port) (net.Listener, error)`; `(*yggStack).Close()`; `subnetAddr(core.Core address) netip.Addr`.
- [ ] Test first: two cores in one process (B listens `tcp://127.0.0.1:0`, A peers to it), each with an `ipv6rwc`; stack on B at its main address with an `ssh` server (x/crypto/ssh, generated key, `NoClientAuth`) answering `exec` with "ok"; stack on A at its subnet address; reader loops feed `tap`; A dials `[B]:22` through its stack and runs a command → "ok". Expect FAIL (undefined).
- [ ] Implement `yggdial.go`; run test until PASS.
- [ ] Commit `feat: TCP stack over a Yggdrasil node`.

### Task 2: wire into the node and SSH dial
**Files:** Modify `go/node.go` (Stop, reader loop), `go/tunnel.go` (readFromYgg), `go/setup.go` (SetupParams, dial)
- [ ] Test first: `TestShouldFallback` (auth error / host key changed → false; dial error, EOF, timeout → true) and `TestDialTimeouts` (with/without yggAddress).
- [ ] Add `Result struct{YggAddress string}` to `SetupParams` (the app already sends the whole profile JSON: no Kotlin change); `Node.yggDial`, global `yggStk atomic.Pointer[yggStack]`, `yggStackTap`; `|| yggStackTap(buf[:k])` in both readers; close in `Stop`; `dialSSH` with fallback and logs «Connecting through Yggdrasil».
- [ ] `go vet ./... && go test -count=1 ./...` PASS; commit `feat: SSH to the server through Yggdrasil when the direct address does not answer`.

### Task 3: verify and prepare 0.40
- [ ] Packet equivalence tests still green; Kotlin `testDebugUnitTest -PskipGo` green; `libygg.so` size noted.
- [ ] Bump 0.40/56, bilingual CHANGELOG + fastlane 56.txt, `assembleRelease`; commit. Stop before release.
