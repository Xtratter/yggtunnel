# YggTunnel for Linux

[Русский](README.ru.md) · **English**

A client for your own server through the [Yggdrasil](https://yggdrasil-network.github.io/) network, the same
Go core and the same `yggtunnel://` profiles as the Android app. Status: **0.1.0, command line and daemon**;
the Qt window, kill switch and split routing are the next steps.

```
yggtunnelctl ──unix socket (JSON)──▶ yggtunneld (root, systemd)
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

## Install (from source)

```sh
sudo install -m755 yggtunneld yggtunnelctl /usr/bin/
sudo install -m644 packaging/yggtunneld.service /etc/systemd/system/
sudo install -m644 packaging/io.github.xtratter.yggtunnel.policy /usr/share/polkit-1/actions/
sudo groupadd -f yggtunnel && sudo usermod -aG yggtunnel "$USER"   # log in again afterwards
sudo systemctl enable --now yggtunneld
```

## Use

```sh
yggtunnelctl import profile.txt     # or: yggtunnelctl import -   (paste the link, Ctrl-D)
yggtunnelctl up
yggtunnelctl status
yggtunnelctl down
yggtunnelctl log
yggtunnelctl panic                  # remove every route, rule and DNS setting the daemon added
```

The profile link holds a private key, so it is never accepted as a command-line argument (the process
list would show it). Changing the connection is authorised through polkit
(`io.github.xtratter.yggtunnel.connect`); the socket `/run/yggtunnel.sock` belongs to the group
`yggtunnel`.

## What it changes on the system

While connected: the interface `yggtun0` (the node's Yggdrasil address `/7`, `clientIp4/32`, `clientIp6/128`,
MTU 1280), default routes in table 51871, two `ip rule` entries (priorities 32763 and 32764), the nftables table
`inet yggtunnel` that marks the daemon's own traffic so it bypasses the tunnel (and masquerades it to the outgoing link's address), and DNS `1.1.1.1` / `8.8.8.8`
on `yggtun0` through systemd-resolved (`/etc/resolv.conf` is never edited).

Every change is recorded in `/var/lib/yggtunnel/prev.json` before it is made and undone in reverse order on
`down`, on any failure, and at the next start if the daemon was killed. `ExecStopPost` runs
`yggtunneld --recover-only` as a safety net. `yggtunneld --dry-run --no-polkit` logs the changes
instead of applying them.

## Not yet

The window, kill switch, split routing (by app and by subnet or domain), peer auto-pick settings, a package
(PKGBUILD). See `docs/superpowers/specs/2026-10-09-linux-client-design.md`.
