# YggTunnel 0.39: refactoring and performance

## Goal
Cleaner code and a faster Go packet path with **zero change on the wire**:
other Yggdrasil nodes, peers and the user's server must not notice anything.

## Compatibility contract (must not change)
- Bytes of every packet we build: IPv6 header (hop limit 64, flow 0), UDP/ICMPv6/ICMP
  headers, checksums (incl. UDP 0 -> 0xffff), echo id `0x5947`, payload sizes.
- WireGuard IPC config, keepalive 25 s, MTU 1280, lane rules (`laneMinSize`, main node for small packets).
- Yggdrasil/ironwood: no change to the patched ironwood, peer URIs and `?priority=`, TLS/QUIC/WSS handling.
- Speed-test protocol v2, server scripts' `YGGTUNNEL_RESULT {json}` contract, JNI names, Kotlin<->Go JSON.
- No new dependencies.

## Safety net (first, before any change)
1. Golden tests: record outputs of the CURRENT `checksum`, `ipv6Header`, `buildUDP`, `parseUDP`,
   `unreachable`, `echo6`, `echo4` on fixed + randomized inputs (seeded, many lengths incl. odd).
   New code must match byte for byte.
2. Benchmarks (`go test -bench -benchmem`) before/after: checksum, buildUDP, deliver, lanes.pick.
3. Existing `go test ./...` and Kotlin `testDebugUnitTest` stay green.

## Go changes
- `packet.go` (new): move checksum/header/UDP/ICMP builders and parsers out of tunnel.go/probe.go;
  one checksum implementation (drop duplicate `sum16`), word-at-a-time summation.
- `buildUDP`/`echo6`/`unreachable`: single allocation, headers written in place.
- `yggBind.deliver`: one allocation instead of three (or a buffer pool if bench proves it).
- `lanes.pick`: lock-free read of the ready set; `lane.watch`: Ticker instead of `time.After`.
- `yggEndpoint`: precompute `As16` once.
- `AttachTunnel`: split into config parsing, WireGuard IPC building, reader goroutine.

## Kotlin changes (audit, then only proven wins)
- Review `Prefs.kt`, `ConnLog.kt`, `Watchdog.kt`, `MainActivity.kt` for repeated parsing/IO on the main thread
  and redundant work in periodic loops.
- Split a file only where the boundary is obvious (candidate: Prefs.kt by area). No behaviour change.

## Out of scope
Protocol changes, lanes redesign, new features, UI changes, server scripts (except if a pure cleanup is trivial).

## Verification and release
- `go vet`, `go test ./...`, benchmark table before/after, Kotlin unit tests, `assembleRelease` in Termux.
- Release 0.39 (bilingual notes) only after your OK on the diff; no install on the phone without you.
- No real IPs/keys/domains in commits; spec stays local until you say push.
