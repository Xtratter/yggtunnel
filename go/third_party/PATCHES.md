# Patched dependencies

YggTunnel builds against **one patched library**: ironwood, Yggdrasil's routing core. Everything else,
yggdrasil-go included, is the upstream release from `go.mod` without changes.

**Read this before updating yggdrasil-go or ironwood.**

## ironwood — pending-bytes counter

| | |
|---|---|
| Upstream | `github.com/Arceliar/ironwood v0.0.0-20260613025018-d50055b11f5e` (the version yggdrasil-go v0.5.14 requires) |
| Copy | `go/third_party/ironwood/`, wired in by `replace github.com/Arceliar/ironwood => ./third_party/ironwood` in `go/go.mod` |
| Patch | `go/third_party/ironwood-yggtunnel.patch` (`patch -p1` inside the upstream module), 22 changed lines |
| Since | YggTunnel 0.25 (2026-10-03) |

### What it does

It adds a counter of **traffic bytes routed to a peer but not yet written to its connection** and exposes
it as `(*network.PacketConn).PendingBytes()`. Through embedding it is reachable as
`(*core.Core).PendingBytes()`.

- `network/core.go`: `core.pending atomic.Int64`.
- `network/router.go` (`handleTraffic`): when the router hands a traffic packet to a peer, `pending += size`.
- `network/peers.go` (`peerWriter.sendPacket`): when a traffic packet has been written, or refused as too
  big, `pending -= size`. In `peer._push`, traffic dropped from a full queue is subtracted too.
- `network/packetqueue.go`: `drop()` records the size of dropped traffic (`droppedTraffic`) for `_push`.
- `network/packetconn.go`: `PendingBytes()`.

### Why

The «parallel links to the server» option (`go/lanes.go`) runs extra Yggdrasil nodes and sends each
WireGuard datagram through the one with the fewest bytes still waiting for its link. Nothing in the
upstream API tells how busy a node's links are.

### What it does not change

The patch only counts. Routing decisions, the packet queue's behaviour, wire formats, handshakes and
encryption are untouched, so a patched node is an ordinary Yggdrasil 0.5 node to every peer.

An earlier attempt (0.25) also changed `router._lookup` to spread one node's packets over several links to
the same peer. It was dropped: ironwood's encrypted sessions (`encrypted/session.go`) reject any packet
whose nonce is not larger than the last one received, so packets of one session must not be reordered.
That is why lanes are separate nodes.

### Tests that cover it

Run from `go/` (see the README for `CGO_CFLAGS`):

```
go test -run 'TunnelLanes|LanePickRoundRobin|LaneURIsDistinct|TunnelEndToEnd' -v .
```

- `TestTunnelLanes` — a second lane comes up, carries WireGuard both ways on its own, is taken out when its
  link drops and comes back, has the main node's MTU, and **`PendingBytes()` returns to zero** after
  traffic (a leak would skew the lane choice).
- `TestLanePickRoundRobin` — the choice between lanes.

### Updating ironwood or yggdrasil-go

1. Update `go.mod` as usual (`go get github.com/yggdrasil-network/yggdrasil-go@vX`) and note the ironwood
   version it requires: `go list -m github.com/Arceliar/ironwood`. Temporarily remove the `replace` line
   to see it.
2. Copy the new upstream module over the old copy:
   ```
   rm -rf third_party/ironwood
   cp -r "$(go env GOMODCACHE)/github.com/!arceliar/ironwood@<version>" third_party/ironwood
   chmod -R u+w third_party/ironwood
   ```
3. Apply the patch: `cd third_party/ironwood && patch -p1 < ../ironwood-yggtunnel.patch`.
   If a hunk fails, re-do the five small changes above by hand. They are independent of each other.
4. Put the `replace` line back, run the tests above and the whole suite (`go test ./...`).
5. Regenerate the patch file from the new copy (`diff -ruN <upstream> third_party/ironwood`), and update
   the version in the table above.

### Related lessons

- Every extra `ipv6rwc.ReadWriteCloser` needs `SetMTU` like the main node (`node.go`). With ipv6rwc's
  default of 1280, a lane's Read answered the server's bigger packets with «packet too big», writing from
  inside Read, and locked up (fixed in 0.29).
