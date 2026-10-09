# YggTunnel for Linux

[Русский](README.ru.md) · **English**

A client for your own server through the [Yggdrasil](https://yggdrasil-network.github.io/) network, the same
Go core and the same `yggtunnel://` profiles as the Android app. Status: **0.4.0, daemon, command line, a Qt window, a kill switch and split routing by subnet and domain**;
routing by application and a package are the next steps.

```
yggtunnel-gui ──┐
                ├─ unix socket (JSON) ─▶ yggtunneld (root, systemd)
yggtunnelctl ───┘
                                         ├─ core     Yggdrasil + WireGuard (shared with Android, go/core)
                                         ├─ netconf  TUN, addresses, routes, ip rules, traffic mark
                                         ├─ dns      systemd-resolved, on the tunnel link only
                                         └─ store    profile (private key encrypted), undo record
```

## Build

Needs Go (the version in `go/go.mod`; `GOTOOLCHAIN=auto` fetches it), no JDK or NDK.

```sh
cd linux
go build -o yggtunneld ./daemon
go build -o yggtunnelctl ./cli
go vet ./... && go test ./...
```

Network tests run inside a throw-away user+network namespace (`unshare -rn`), so they never touch the
host network; they are skipped with a message where namespaces are unavailable.

### The window

Run these from the `linux/` directory. Needs Qt 6 (`qt6-declarative`, `qt6-tools`), CMake and Ninja.

```sh
cmake -S gui -B gui/build -G Ninja && cmake --build gui/build
ctest --test-dir gui/build        # offscreen; writes screenshots to gui/build/shots
gui/build/yggtunnel-gui
```

The window shows the state, the server and the peers, connects and disconnects, imports a profile (paste a
link or open a file), shows the connection history and the node log, and has «Disable everything»
(`panic`). It talks only to the daemon, so your user must be in the group `yggtunnel`. English and Russian
follow the system language; `YGGTUNNEL_SOCKET` overrides the socket path.

## Install (from source)

```sh
sudo install -m755 yggtunneld yggtunnelctl /usr/bin/
sudo install -m644 packaging/yggtunneld.service /etc/systemd/system/
sudo install -m644 packaging/io.github.xtratter.yggtunnel.policy /usr/share/polkit-1/actions/
sudo groupadd -f yggtunnel && sudo usermod -aG yggtunnel "$USER"   # log in again afterwards
sudo systemctl enable --now yggtunneld
sudo cmake --install gui/build --prefix /usr   # the window, its menu entry and icon
```

## Use

```sh
yggtunnelctl import profile.txt     # or: yggtunnelctl import -   (paste the link, Ctrl-D)
yggtunnelctl up
yggtunnelctl status
yggtunnelctl down
yggtunnelctl log
yggtunnelctl killswitch on          # drop traffic that bypasses the tunnel while connected (off by default)
yggtunnelctl lan off                # with the kill switch: also drop local-network traffic (allowed by default)
yggtunnelctl split mode only        # all | exclude | only — which destinations use the tunnel (disconnected only)
yggtunnelctl split add 203.0.113.0/24   # a subnet, an address or a domain (example.com)
yggtunnelctl split remove example.com
yggtunnelctl split show
yggtunnelctl panic                  # remove every route, rule and DNS setting the daemon added
```

The profile link holds a private key, so it is never accepted as a command-line argument (the process
list would show it). Changing the connection is authorised through polkit
(`io.github.xtratter.yggtunnel.connect`); the socket `/run/yggtunnel.sock` belongs to the group
`yggtunnel`.

## Kill switch

With `killswitch on`, while the tunnel is up, every packet this machine sends that is **not** going through
`yggtun0` is dropped, so a lost tunnel route can never leak traffic onto the physical link. Always let through:
loopback, the tunnel itself, the daemon's own traffic (the peers and the server; it is recognised by its
cgroup), DHCP, IPv6 neighbour discovery, and — unless you run `lan off` — local-network destinations
(`10/8`, `172.16/12`, `192.168/16`, `169.254/16`, multicast, limited broadcast, `fe80::/10`, `fc00::/7`).

It is armed after the tunnel is attached and removed by `down`, by a failed `up`, by `panic`, and when
the daemon stops. The window has the same two switches in the «Protection» card. Check it by hand with
`sudo nft list table inet yggtunnelks` (the counter on the `drop` rule shows what it stopped).

Limits, on purpose: if the daemon is killed or crashes, the kill switch is removed (`ExecStopPost`) — a
crash must never lock you out of the network; traffic that is forwarded through this machine (containers,
virtual-machine bridges) is not covered; nothing is dropped before `up` completes.

## Split routing

Three modes (`split mode`): **all** — everything goes through the tunnel (the default); **exclude** — everything
except the list; **only** — nothing except the list. The list holds subnets (`203.0.113.0/24`, a bare
address is a `/32`, IPv6 works) and domain names. The same card is in the window («Routing»).

- The mode can be changed only while disconnected; the lists can be changed at any time and apply at once.
- Domains are resolved by the daemon with the system resolver every 60 seconds and whenever the list changes.
  Their addresses stay in the firewall for 5 minutes. A name that fails to resolve keeps its last addresses and
  the problem is shown in `status`. **Subdomains are not included** — list each name; names that are served from
  many addresses (CDNs) may be answered differently between lookups.
- In **only** mode traffic to the list is marked and routed into the tunnel, the rest goes directly; the tunnel's
  DNS servers are always reached through the tunnel, and only the listed names are asked there. The daemon's own
  traffic never enters the tunnel, even to a listed address.
- With the **kill switch** in **only** mode it protects exactly what should be tunnelled: if the tunnel route
  disappears, traffic to the list is dropped; everything else keeps going where it was meant to.
- Entries are checked: at most 500 subnets and 500 domains; default routes (`0.0.0.0/0`, `::/0`), anything that
  overlaps `200::/7` or the loopback ranges, wildcards and bare top-level names are refused.
- Check by hand: `ip rule`, `sudo nft list table inet yggtunnel` (the chain `split` and the sets `names4`, `names6`).

Not included yet: routing by application.

## What it changes on the system

While connected: the interface `yggtun0` (the node's Yggdrasil address `/7`, `clientIp4/32`, `clientIp6/128`,
MTU 1280), default routes in table 51871, two `ip rule` entries (priorities 32763 and 32764), the nftables table
`inet yggtunnel` that marks the daemon's own traffic so it bypasses the tunnel (and masquerades it to the outgoing link's address), and DNS `1.1.1.1` / `8.8.8.8`
on `yggtun0` through systemd-resolved (`/etc/resolv.conf` is never edited). With the kill switch: the nftables table
`inet yggtunnelks`.

Every change is recorded in `/var/lib/yggtunnel/prev.json` before it is made and undone in reverse order on
`down`, on any failure, and at the next start if the daemon was killed. `ExecStopPost` runs
`yggtunneld --recover-only` as a safety net. `yggtunneld --dry-run --no-polkit` logs the changes
instead of applying them.

## Not yet

Routing by application, peer auto-pick and lane settings in the window, a tray icon, a package
(PKGBUILD). See `docs/superpowers/specs/2026-10-09-linux-client-design.md`.
