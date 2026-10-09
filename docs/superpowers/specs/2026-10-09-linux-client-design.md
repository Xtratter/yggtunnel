# YggTunnel for Linux — design

Date: 2026-10-09. Status: draft for review.

## Goal

A desktop client for Linux (target: Manjaro x86-64, KDE/Wayland) that connects to the user's own
server through Yggdrasil, using the same Go core and the same `yggtunnel://` profile as the Android
app. Full window UI in the style of the Android app.

## Decisions (agreed)

| Topic | Decision |
|---|---|
| Interface | Full window (status, peers, routing, settings, log) |
| UI toolkit | Qt6 + QML, thin C++ layer |
| Repository | `linux/` directory in this repository; core shared |
| First release scope | Full tunnel, DNS through the tunnel, profile import, peer selection, log, kill switch, split routing (by app and by subnet/domain) |
| Packaging | PKGBUILD (local `makepkg`, AUR later) |
| System changes | All in Go inside the daemon: netlink, nftables library, D-Bus (`systemd-resolved`) |

## Architecture

```
GUI (Qt6/QML, user) ──JSON over unix socket──▶ yggtunneld (root, systemd)
yggtunnelctl (CLI)  ──────────────────────────▶   ├─ core     Yggdrasil + wireguard-go
                                                  ├─ netconf  TUN, routes, DNS
                                                  ├─ firewall kill switch, marks
                                                  ├─ split    per-app / per-subnet rules
                                                  ├─ store    profiles, secrets, saved network state
                                                  └─ ipc      socket, commands, event stream
```

Layout:

```
go/                 shared core
  core/             NEW: library package (moved from package main, no cgo)
  jni.go            Android only, imports core
linux/
  daemon/           yggtunneld
  cli/              yggtunnelctl
  gui/              yggtunnel-gui (C++/Qt6 QML, CMake)
  packaging/        PKGBUILD, systemd unit, polkit rules, .desktop
```

### Core extraction (the only change to shared code)

All core files are `package main` today and cgo exists only in `go/jni.go`. Move the platform-neutral
files (`node`, `tunnel`, `lanes`, `packet`, `peers`, `peertest`, `speed`, `probe`, `tcpdiag`,
`yggdial`, `setup`, `hang`, `log` and their tests) into `go/core`, export what the daemon and
`jni.go` need, keep `jni.go` as the only `package main` file. Constraints:

- Wire format frozen: `TestPacketEquivalence` and `packet_ref_test.go` move unchanged and stay green.
- Patched ironwood (`go/third_party`, `PATCHES.md`) untouched.
- Android build (`go/build.sh`, Gradle) keeps working; verified by the Go tests and by building the
  library, not by installing on a phone.
- The tunnel already wraps a file descriptor. On Linux the daemon opens `/dev/net/tun`, names it
  `yggtun0` and passes the descriptor in, so tunnel code does not change.

## Daemon behaviour

### States

`Off → Starting → Connected → Reconnecting → Off`, plus `Error(reason)`. Same names and events as the
Android app, so the status island and the log behave identically.

### Connect (`up`)

1. Save current network state to `/var/lib/yggtunnel/prev.json` (default route, DNS, our nftables rules).
2. Core starts the Yggdrasil node and picks peers.
3. Create `yggtun0`, bring up WireGuard inside Yggdrasil.
4. Routes: default route via `yggtun0`; server and peer addresses stay on the previous gateway
   (otherwise tunnel packets loop).
5. DNS: server's DNS on `yggtun0` only, through `systemd-resolved` over D-Bus.
6. Enable kill switch and split-routing marks.
7. State `Connected`; event to clients.

### Rollback

Any failing step runs the inverse of the completed steps in reverse order and restores `prev.json`.
The same rollback runs on `down` and on clean daemon stop. If the daemon was killed, the next start
finds unfinished state and rolls it back.

### Kill switch

nftables rule drops outgoing traffic not going through `yggtun0`, except: peers and server, loopback,
local network (configurable), DHCP. It stays during `Reconnecting` and is removed only by explicit
`down` (otherwise an outage would leak direct traffic). Escape hatch: `yggtunnelctl panic` and a
«Disable everything» button remove all our routes, rules and DNS settings.

### Split routing

- **Subnets/domains:** domains resolved by the daemon, results kept in an nftables set refreshed by
  TTL. Modes: «only these go through the tunnel» / «everything except these».
- **Apps:** the GUI starts the chosen app with `systemd-run --user --scope` in its own cgroup v2;
  nftables marks traffic by cgroup and routes it through the tunnel or around it. A running app must
  be restarted (shown in the GUI). Flatpak/Snap are not guaranteed in the first release.

### Server setup

The embedded scripts (`server.sh`, `devices.sh`, `wrapper.sh`, `store.sh`, `speed.sh`) run from the
daemon over SSH, as `ServerCall` does on Android; progress is streamed to the GUI. Last output line
stays `YGGTUNNEL_RESULT {json}`.

## Security

- Socket `/run/yggtunnel.sock`, mode 0660, group `yggtunnel`. State-changing actions (connect,
  disconnect, change routing) are authorised through polkit.
- Secrets in `/var/lib/yggtunnel/`, root-owned, mode 0600, encrypted with a key held by the daemon.
  The GUI never reads secrets, only masked values.
- The GUI never runs anything as root itself.

## GUI

One window, Material 3 look from the Android app, KDE theme used for fonts and dark mode:

- status card with the connect button and the status island drawn inside the window (a separate
  overlay is not available on Wayland); desktop notification through D-Bus when the window is hidden;
- peers card: latency, speed, pinning, test;
- routing card: full tunnel, subnet/domain mode, app list from `.desktop` files, kill switch,
  «Disable everything»;
- settings: main link, parallel lanes (default 1), MTU;
- log with tabs «Connection» and «Node»;
- profile import from `yggtunnel://` link or QR image; server list with deletion.

A tray icon is secondary.

## Testing

- Go: unit tests for netconf and firewall in a network namespace (`unshare -n`), rollback tests, and
  a dry-run mode that prints actions without applying them.
- Existing core tests move with the code and stay green.
- Qt: tests for models and event parsing without a daemon.
- Integration: daemon in a namespace with stub peers; egress-IP check as in the Android lessons.
- Before a release the window is started and checked with screenshots.
- No real addresses, ports, Ygg addresses, domains or keys in code or tests; only examples.
- System settings of the laptop (routes, systemd, nftables) are changed only after a warning and
  confirmation; the network can be lost.

## Delivery stages

1. Core extraction to `go/core`, daemon skeleton builds.
2. netconf, store, ipc, `yggtunnelctl`; full tunnel with rollback.
3. Qt window.
4. Kill switch.
5. Split routing (subnets/domains first, then apps).
6. PKGBUILD, bilingual docs (EN + RU), release.

Each stage is a version and a release, per the repository rules.

## Out of scope for the first release

AppImage/deb/rpm, Flatpak/Snap app routing guarantees, running without root (`CAP_NET_ADMIN`),
GNOME/other-desktop tuning, AUR upload.

## Open risks

- Core extraction touches many files; mitigated by moving without logic changes and keeping the tests.
- Cgroup-based app routing depends on how the app is launched; verified on KDE first.
- Go is not installed on the laptop yet (`pacman -S go` needed with the user's permission).
