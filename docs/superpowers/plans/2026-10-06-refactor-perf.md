# YggTunnel 0.39 refactoring and performance — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (chosen: native, no subagents). Steps use `- [ ]`.

**Goal:** cleaner code and a faster Go packet path with byte-identical wire behaviour.
**Architecture:** freeze the current packet functions as reference implementations in a `_test.go` file, compare new code to them on random input, benchmark before/after, then refactor behind those tests.
**Tech Stack:** Go (wireguard-go, yggdrasil-go 0.5.14), Kotlin. Termux arm64 build.
**Spec:** docs/superpowers/specs/2026-10-06-refactor-perf-design.md

## Global Constraints
- Wire format unchanged: IPv6 hop limit 64, UDP csum 0→0xffff, echo id 0x5947, MTU 1280, `laneMinSize`=400.
- No new dependencies; JNI names, JSON, speed protocol v2, `YGGTUNNEL_RESULT` untouched.
- No real IPs/keys/domains in commits. Commit author = repo's existing (noreply). Nothing pushed.

## Review Focus
- Odd/zero/huge payload lengths in checksum/buildUDP (0, 1, 1279, 65535).
- Checksum that comes out 0 for UDP must become 0xffff (and stay 0 for ICMPv6).
- `parseUDP` on truncated/oversized length fields must still reject.
- `deliver` on full channel must still count `bindDrops` and not block.
- `pick` with no ready lanes returns nil; lane going down/up under concurrent `Send`.

## File map
- Create `go/packet.go` (checksum, ipv6Header, buildUDP, parseUDP, unreachable, echo6, echo4 moved from tunnel.go/probe.go)
- Create `go/packet_ref_test.go` (frozen reference copies `refChecksum`, `refBuildUDP`, `refParseUDP`, `refUnreachable`, `refEcho6`, `refEcho4`) and `go/packet_test.go` (equivalence + benchmarks)
- Modify `go/tunnel.go`, `go/probe.go`, `go/lanes.go`; Kotlin per Task 6.

### Task 1: Safety net (reference + equivalence tests + baseline benchmarks)
**Files:** Create `go/packet_ref_test.go`, `go/packet_test.go`
**Produces:** `ref*` functions (verbatim copies of current code); `TestPacketEquivalence`; `Benchmark{Checksum,BuildUDP,Deliver,Pick}`.
- [ ] Copy current `checksum`, `ipv6Header`, `buildUDP`, `parseUDP`, `unreachable`, `echo6`, `sum16`, `echo4` into `packet_ref_test.go` renamed with `ref` prefix (verbatim bodies).
- [ ] Write `TestPacketEquivalence`: seeded `rand.New(rand.NewSource(1))`, lengths {0,1,2,3,39,40,41,400,1279,1280,1500,65535} plus 2000 random lengths; for each compare `checksum` vs `refChecksum` (protos 17, 58), `buildUDP` vs `refBuildUDP`, `parseUDP` vs `refParseUDP` (also on mutated/truncated packets), `unreachable` vs `refUnreachable`, `echo6`/`echo4` vs refs. At this point the functions are the same, so it passes.
- [ ] Write benchmarks with `b.ReportAllocs()`: checksum(1280 B), buildUDP(1200 B), `yggBind.deliver` (drain channel in loop), `lanes.pick` (3 ready lanes).
- [ ] Run `cd go && go test -run TestPacketEquivalence -count=1 ./... ` → PASS; `go test -run xxx -bench . -benchmem | tee $CLAUDE_JOB_DIR/tmp/bench-before.txt`.
- [ ] Commit: `test: reference packet implementations, equivalence tests, baseline benchmarks`.

### Task 2: packet.go — move and optimise
**Files:** Create `go/packet.go`; Modify `go/tunnel.go:50-125`, `go/probe.go:96-132`
- [ ] Move the functions to `packet.go` unchanged; remove `sum16` by making `echo4` use `checksumBytes(b)`; `go test` stays green; commit `refactor: move packet code to packet.go`.
- [ ] Optimise: `checksum` sums 8 bytes at a time with carry folding via `binary.BigEndian.Uint64` (uint64 accumulator, fold at end), no closure; `buildUDP`/`unreachable`/`echo6` allocate one `[]byte` of final size and write IPv6 header (`putIPv6Header(dst []byte, src, dst netip.Addr, proto byte, n int)`) in place. Keep `ipv6Header` only if still used.
- [ ] `go test -run TestPacketEquivalence -count=1` PASS (byte-identical); bench → `bench-after-packet.txt`; commit `perf: single-allocation packet builders, wide checksum`.

### Task 3: bind, endpoint, lanes
**Files:** Modify `go/tunnel.go` (yggBind, yggEndpoint), `go/lanes.go`
- [ ] `deliver`: one allocation `m := make([]byte, 18+len(data))`; `copy(m, from.Addr().As16())`, put port, copy data (same 18-byte layout the receiver parses; addr is always 16 bytes — `from` is IPv6). Keep non-blocking select and `bindDrops.Add(1)`.
- [ ] `lane.watch`: `time.NewTicker(time.Second)` with `defer Stop()`.
- [ ] `lanes.pick`: keep logic and semantics; replace mutex with `atomic.Pointer[[]*lane]` for `all` (copy-on-write in `startLanes`/`close`) and atomic counter for `next`. Behaviour (ready filter, least pending, round-robin ties) identical; extend `multilane_test.go` expectations unchanged.
- [ ] `go vet ./... && go test -race -count=1 ./...` PASS; bench `bench-after-lanes.txt`; commit `perf: leaner deliver, lock-free lane pick, ticker in watch`.

### Task 4: AttachTunnel split
**Files:** Modify `go/tunnel.go:300-400`
- [ ] Extract `parseTunnelConfig(cfg) (priv, pub string, server netip.Addr, err)`, `wgIPC(priv, pub string, server netip.Addr, port int) string`, `(n *Node) readFromYgg(...)` (the reader goroutine). Behaviour and log lines identical. Add `TestWgIPC` asserting the exact string (keepalive 25, allowed_ip 0.0.0.0/0 and ::/0).
- [ ] `go vet`, `go test -race ./...` PASS; commit `refactor: split AttachTunnel`.

### Task 5: Go-side leftovers audit
- [ ] Grep `go/*.go` for per-call allocations in loops (peertest.go, speed.go, tcpdiag.go) with `go test -bench` only where a measurable hot spot exists; change only proven wins, tests green; commit or record "nothing found".

### Task 6: Kotlin audit
**Files:** `Prefs.kt`, `ConnLog.kt`, `Watchdog.kt`, `MainActivity.kt`
- [ ] Read for repeated JSON/SharedPreferences parsing, disk IO or heavy work on the main thread, redundant periodic work; fix proven issues keeping behaviour; add/keep unit tests; split `Prefs.kt` by area only if the boundary is clean. `./gradlew --no-daemon testDebugUnitTest -PskipGo` PASS; commit per change.

### Task 7: Verify and prepare 0.39
- [ ] `go vet ./... && go test -race -count=1 ./...`; Kotlin unit tests; benchmark table before/after.
- [ ] Bump versionCode/Name to 0.39 first, build `./gradlew --no-daemon assembleRelease`, write bilingual notes. **Stop before `release.sh`/push: show the user the diff and numbers.**
