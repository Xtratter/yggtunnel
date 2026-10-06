# Changelog

[Русский](CHANGELOG.ru.md) · **English**

## 0.39 — 2026-10-06

- Refactoring and speed-up of the packet path, **nothing changes on the wire**: the bytes sent to Yggdrasil nodes and the server are identical to 0.38 (checked byte for byte against the old code on thousands of random packets)
- Faster packet building: one memory allocation instead of two, checksum ~2× faster (UDP packet build 3.8 → 1.7 µs); fewer allocations for datagrams received from the server
- The packet code moved to `packet.go`; `AttachTunnel` split into small functions with a test for the WireGuard configuration
- The connection log summary no longer recompiles a regex for every line

## 0.38 — 2026-10-04

- **Delete any server from the list**: every server in Server → Servers has «✕» next to «Edit»; it forgets that server on this phone (the server itself is not touched). Before, only the active server could be deleted, from its card
- The wrapper setup no longer says «Server ready» when it still needs an answer (a domain for a new site, the telemt limits, which site): the log window now says «One more step — answer in the next window»
- Checked on a fresh VPS by the user: setup by password works, the app switched to its own SSH key

## 0.37 — 2026-10-04

- **Set up a server by password**: the server dialog has «Log in: By password / By SSH key»; a new server starts with password, what a fresh VPS usually comes with. After a successful setup the app makes its own ed25519 key, adds it to the server's ~/.ssh/authorized_keys, checks that it logs in with it, and uses the key from then on; for root the password is not kept. Both password logins servers use are supported (password and keyboard-interactive); a refused password gets a clear message
- A user other than root with sudo that asks for a password: the password is kept encrypted and given to sudo (sudo -S, only after checking that sudo really asks — otherwise the line would reach the script)
- The password never leaves in the settings copy saved on the server
- Tests: setup by password on a test SSH server (key added and checked, then the key alone works, sudo -S for a user, a wrong password); the password stays out of the server copy

## 0.36 — 2026-10-04

- **A name at deSEC right from the app**: when the server has no HTTPS site yet, «Site for the wrapper» now offers «Create a name at deSEC». Paste your deSEC (desec.io, free) API token and pick a name — a new one like myvpn.dedyn.io or one under a domain you already have there (vpn.yourname.dedyn.io). The server registers the name, points its A record to itself (with the domain's minimum TTL), then installs nginx, gets the Let's Encrypt certificate and sets the wrapper up, waiting up to 3 minutes for the name to work. The calls to deSEC are made by the server (it always has internet); the token goes there only over SSH stdin, is kept encrypted on the phone, and is left out of the settings copy saved on the server
- The «Site for the wrapper» dialog shows the server's own IPv4 address for the A record
- Tests: deSEC against a fake API (new name, a name under an owned domain, the minimum TTL, a foreign domain, a taken name, a bad token); the token stays out of the server copy

## 0.35 — 2026-10-04

- Connection log: switching the check interval while connected (10 → 30 s) no longer logs «No checks for 30 s» at every check — the pause rule now follows the current setting (it kept the one from connecting); a test covers it
- Shorter log lines: the direct check is just «direct 87» (the host is in the server card), the pause note is shorter

## 0.34 — 2026-10-04

- **The patched dependency is documented**: go/third_party/PATCHES.md (and PATCHES.ru.md) — what the ironwood patch does and does not change, why, which tests cover it and how to carry it over to a new ironwood or yggdrasil-go; the patch itself is kept as go/third_party/ironwood-yggtunnel.patch (re-applies cleanly to the upstream module); the README links to it
- **Server setup checked against the live server**: the firewall script and its unit come out byte for byte the same as on the server, the speed test sender and its unit too; the Yggdrasil and WireGuard settings, services, /etc/yggtunnel and the site's include match. The one thing done by hand earlier — a per-address limit of new connections to port 443 in a third-party firewall (mtpr-synfix) — is now detected by the wrapper setup, which warns when it is too low for the phone's reconnects
- Help texts, store descriptions and the changelog reworded and tidied

## 0.33 — 2026-10-04

- **The connection log screen is only the log now**: newest first, grouped by day («Today», then dates), times only, outages red, recoveries green, checks dimmed; on top — the last 24 hours' checks and outages, and «Events / Everything». The log's settings and the diagnostics (speed measurement, upload diagnostics, problem report) moved to their own dialogs behind «Settings» and «Diagnostics»
- Fixed: the log opened on the oldest records (auto-scroll only worked once already at the end) and was re-read and redrawn in full every second (up to 2 MB, ~4000 lines, on the main thread — jerky, selection lost). Now it is re-read every 3 s, only when the file changed and while the list is at the top; no scrolling inside a scrolling dialog
- Tests: the log view (newest first, days, limits, the last 24 h, the year boundary)

## 0.32 — 2026-10-04

- **Every settings change goes into the connection log**: «Setting: Parallel links to the server → 3», «Main link to the server → TLS», auto-pick, switches, theme, the app list, the peer list (number of addresses, from the catalog or by hand), server changed — so a measurement can be matched with what was set at the time. Secrets are never written (keys and server data: only «changed»); peer-test filters and timestamps are skipped
- «VPN on» now says which link and how many: «VPN on (through the server, link TLS, links: 3)»

## 0.31 — 2026-10-04

- Connection log: a session that ended without «VPN off» is noted at the next start — «The previous session ended without «VPN off»: the app was updated, the system closed it, or it crashed». Before, an update (the system kills the old process) left the checks simply stop and «VPN on» follow as if nothing happened. Checked on the phone for 0.30: checks every 10–11 s, no false outage after connecting (the first check still failed — WireGuard needed >8 s — and the two-miss rule kept it out of the events)

## 0.30 — 2026-10-04

- **Connection log fixed** (checked against the phone's real log, 3787 lines):
  - no more false «does not answer» after every reconnect: the first check now waits 8 s (was 3 s — WireGuard had not shaken hands yet)
  - a target counts as down only after 2 misses in a row; one lost probe under a full line (a speed test) still shows as «—» in the check line but is no outage; the outage is dated from the first miss
  - stuck probes no longer hold up the checks: each probe runs on its own thread with a deadline (a pool of 3 filled up and checks came every 30–50 s instead of 10)
  - when checks did not run (the phone slept, the system paused the app) the log says so: «No checks for N s»
  - the direct check is labelled «direct <host>» (was «IP <host>»)
- Upload diagnostics: connections are told apart by local port — with lanes all go to the same server address, and their counters were mixed up
- Tests: the log's logic (JVM), connections to one address told apart (Go)

## 0.29 — 2026-10-04

- **Fixed the hang with parallel links** — found by 0.28's hang report: the main thread waited in Stop for WireGuard, WireGuard's sender waited in a lane's Write, and that lane's Read had locked up. The extra lanes kept Yggdrasil's default MTU of 1280 (the main node has 65535); the server's WireGuard datagrams are bigger, and for a packet over the MTU the Read answers «packet too big» by writing from inside Read — which waits on ironwood, which waits on that same Read. So every big packet on a lane was dropped (also why download through lanes fell in 0.26) and sooner or later a lane locked up, taking WireGuard and the Disconnect button with it. Lanes now get the main node's MTU; a test checks it

## 0.28 — 2026-10-04

- **A dead lane is no longer used.** A lane was marked ready once and never taken out: when its link dropped (a network change, a cut connection) every third data packet still went into it and was lost, and the connections inside the tunnel stalled — likely the «app hangs» reported. Now each lane is checked every second: link down → out of use at once; link back → in use again after a ping through it reaches the server
- **Hang reports**: a watchdog checks every second that the app's main thread answers; after 5 s without an answer the stacks of every thread and goroutine go to files/hang-*.txt, the connection log and your server (/etc/yggtunnel/diag/)
- **Go crashes are kept**: a panic or fatal error in the Go core is written to a file and shown and sent to the server on the next start
- Connection log → «Send a problem report»: the same stacks plus the node's state and logs, when the tunnel stalls but the screen works

## 0.27 — 2026-10-03

- **Parallel links: only data goes round the lanes.** 0.26 with 3 links: upload 24.5–30.3 Mbit/s (1 link: 15.4–17.6) — the best through the VPN so far — but download fell to 36–46 (1 link: 90–128): the server's WireGuard answers the address it heard last, and the ACKs of a download came from every lane, so the download hopped between them and arrived out of order. Now WireGuard datagrams under 400 bytes (ACKs, keepalives) always leave through the main node; only data is spread

## 0.26 — 2026-10-03

- **Lanes take turns**: 0.25 sent each packet through the least loaded lane and the main node won every tie — and the queues are almost always empty, so the server got 75.9 MB from the main node and only 26.4 and 12.8 MB through the two lanes, and the upload did not change (17.5 → 19.5 / 11.2 Mbit/s). A lane that only gets the overflow never grows its TCP window. Now ties go round the ready lanes, so all connections carry traffic and grow their windows in parallel

## 0.25 — 2026-10-03

- **Parallel links to the server** (Peers → «Parallel links to the server»: 1–4, default 1 — as before). The whole tunnel rode one TCP connection; on a lossy mobile uplink it halves its window at every loss and grows back by one segment per round trip (~1.4 Mbit/s per second at 80 ms), so a 15 s upload test never got far (diagnostics 03.10). With 2–4 links the phone runs extra Yggdrasil nodes, each with its own key and TLS connection to the server, and WireGuard sends every datagram through the one with the fewest bytes still waiting. Separate nodes because Yggdrasil's sessions drop any packet that arrives out of order — the packets of one session cannot be spread over links; WireGuard itself takes reordering. The server needs no change. Experimental — compare the upload with 1 and with 3–4
- Yggdrasil's routing library (ironwood) is now a patched copy in go/third_party: it counts the bytes waiting for its links (22 lines)
- Tests: a second lane comes up and carries WireGuard both ways alone; lanes with the same address stay separate links; the waiting-bytes counter returns to zero

## 0.24 — 2026-10-03

- **Short send queue on the link to the server**: the phone's kernel kept up to 1.9 MB of not-yet-sent tunnel data in the link socket — about 4 s of queue on a slow, lossy mobile uplink, so under upload everything in the tunnel saw seconds of delay (Speedtest's loaded ping 4150 ms) and its TCP collapsed. Now at most 128 KB stays unsent (TCP_NOTSENT_LOWAT, applied to every link socket); the rest waits in Yggdrasil, whose small queue drops early — a quick signal for the connections inside. Verified on this phone's kernel: unsent data 1.58 MB → 32 KB in a test
- Diagnostics: «Send queue of the link» — 128 KB / no limit, for comparison

## 0.23 — 2026-10-03

- **Upload diagnostics** (Connection log → «Upload diagnostics»): for 2 minutes, once a second, the phone kernel's view of the link to the server — TCP congestion window and threshold, losses, round trip, delivery rate, buffer limits — plus WireGuard traffic and datagrams dropped between Yggdrasil and WireGuard; the log goes to the server (/etc/yggtunnel/diag/). Upload through the VPN is 9–15 Mbit/s against 64 without it, and the server sees the phone's sending rate grow linearly by ~1.7 Mbit/s every second
- The congestion control of the current link can be switched between westwood (this phone's default) and reno for comparison

## 0.22 — 2026-10-03

- **Pick peers: every peer is pinged, the speed only for the best by ping.** The limit chips are now «Speed of the best by ping» (10 / 20 / 50 / 100 / all): all chosen peers connect and ping (24 at once), then the speed is measured one at a time for the N with the fewest losses and the lowest ping — saves time and the server's traffic. Two stages in the progress: «Ping: x of y», then «Speed of the best by ping: x of y»
- **One style for the Peers card**: the «Pick» and «Edit» buttons became rows like in Settings («Pick peers», «Edit the list» with the number of addresses), and the peer settings moved here from Settings: auto-pick, main link to the server, public peers only as a fallback, always use our server's peers

## 0.21 — 2026-10-03

- **TLS is the main link to the server again; Settings → «Main link to the server»** (TLS / QUIC / wss): with QUIC preferred (0.19–0.20) Speedtest through the VPN fell to 1.75 Mbit/s down (48.5 over TLS, 163 without VPN) — UDP is slower on this network, or QUIC lacks socket buffers on the phone. The chosen link gets priority 0, the others stay up as fallbacks
- **Links are recognised by their address without options**: Yggdrasil reports a connected link without ?key= / ?priority=, so the peer list showed the server's tls and wss twice («not connected» and «not in the list»), and auto-pick dialed parked peers again without their ?key=. Both fixed (Go picker and the Peers card)

## 0.20 — 2026-10-03

- **Faster peer test**: a pipeline — 24 peers connect and ping at once (was 8 for everything); peers that reached the server queue (up to 8, nodes kept up) for the speed measurement, which runs **one at a time**: measured together they shared the phone's line, and 0.18–0.19 showed impossible speeds (164 Mbit/s on a ~110 Mbit/s line)
- **Progress bar** in Pick peers: light — connected and pinged, solid — finished with the speed; done / total and the time left

## 0.19 — 2026-10-03

- **QUIC is the preferred link to your server**: the server's own peers now carry Yggdrasil priorities — quic 0, tls ?priority=1, wss ?priority=2. Among several links to the same node Yggdrasil sends over the one with the lowest priority, and both ends honour it, so traffic goes over QUIC (UDP — no TCP inside TCP under WireGuard) and falls back to TLS, then wss, when QUIC is down
- Peer lists saved by older versions are matched without the options, so the server's peers are not added twice

## 0.18 — 2026-10-03

- **Speed test that measures the link, not the losses** (protocol 2 — update it on the server: Peers → Pick → «Update the speed test on the server»): the phone reports every 100 ms what arrived; the server starts at 8 Mbit/s, doubles while under 3% is lost and falls back to what arrived on loss. The result is the rate of the last 40% of the data (or the best full 200 ms window), so 2 MB are enough. On a simulated 5 / 20 / 40 / 60 Mbit/s link it measures 4.8 / 19.2 / 38.8 / 58–61. The old sender blasted at 200 Mbit/s and lost 84–92% in Yggdrasil's queues (4–9 Mbit/s shown)
- The connection log pauses its checks while a speed measurement or a peer test fills the link — no more false drops
- The log in the Connection log window scrolls by itself again (it was inside the dialog's own scroll)
- Measured on the phone (Speedtest through the VPN, mobile network): 0.16 — 33 Mbit/s down, 0.17 with public peers as a fallback — 44.5; the server now sends the phone's data over the direct QUIC link

## 0.17 — 2026-10-03

- **Public peers only as a fallback** (Settings, on by default): while a direct link to your server (tls, quic or wss) is up, public peers are parked. The server's statistics showed why: Yggdrasil 0.5 picks the next hop by link latency, so the server sent ~200 MB of the phone's downloads the long way through a 5 ms public peer instead of the direct 80 ms mobile link — slower and through other people's nodes. When the direct link is lost, the public peers connect again within 5–15 s

## 0.16 — 2026-10-03

- **Peer catalog through your server**: the app itself is outside the VPN, and the catalog site may be unreachable directly — then the server fetches the list and hands it over SSH (Pick shows «through your server»); also for the editor's Catalog and the weekly refresh
- **The server describes itself in plain JSON**: /etc/yggtunnel/server.json (root only) — Yggdrasil address, listeners and peers, WireGuard port, key and devices, the wss wrapper, the speed test, IPv6, saved phones; rewritten after every setup, device, wrapper or speed-test change
- **Phone settings on the server** (Settings → Backup): «Save to the server» / «Restore from the server» — plain JSON in /etc/yggtunnel/clients/<phone>.json, without SSH keys; restoring keeps the SSH keys this phone has
- Restoring a backup shows the server name again (0.12+ backups)

## 0.15 — 2026-10-03

- **Real speed test**: a tiny service on the server (Peers → Pick → «Install the speed test on the server», over SSH) — UDP only on the server's Yggdrasil address, only with a secret the app keeps, at most 1 GB a day. Pick measures every tested peer with 2 MB from your server through that peer (the traffic estimate is shown before the test) and puts the fastest first; the rough ping-burst estimate of 0.14 is gone
- **Connection log → Measure speed (2 MB)**: through Yggdrasil to the phone, on request only; the result goes into the log
- Server panel shows the speed test service; setting the server up again keeps it

## 0.14 — 2026-10-03

- **Pick peers** (Peers → Pick): the whole public peer list (≈370) with filters — transports (tls, quic, wss, tcp, ws), working only — and a test of up to 20 / 50 / 100 / all of them from the phone. Each peer is tried through its own temporary Yggdrasil node (the running connection is untouched): link time, ping and loss to your server through that peer, and a rough speed. The best N (3–15, your choice) are ticked; Use makes them the peer list, our server's peers stay first
- **Connection log** (Settings, on by default): while connected, every 5 / 10 / 30 / 60 s — 8.8.8.8 through the tunnel, the server through Yggdrasil and the server's public address directly. Drops are marked with their length; VPN on/off, reconnects and network changes (Wi-Fi / mobile) too. Only drops or everything, Copy, Clear; kept on the phone (~2 MB)
- The tunnel's own pings are built in the Go core, since the app itself is outside the VPN; their replies never reach apps

## 0.13 — 2026-10-03

- **Our server's peers are always in the list**: direct TLS, now also **QUIC** on the same port, and the wss wrapper — first, never parked by auto-pick, and back even if removed from the list. Settings → «Always use our server's peers» (on by default) turns it off
- **Connect when the app opens** (Settings, off by default): as if you pressed Connect; not when opened by a profile link or after a crash

## 0.12.1 — 2026-10-03

- **Edit a saved server**: Server → Servers → «Edit» next to each server — change host, port, user or key; «Save» only stores the details, «Set up» runs the setup (and makes that server active)
- **The saved SSH key is never shown**: the key and passphrase fields stay empty with «saved» in them — nothing to copy out; leave them empty to keep the key, paste or pick a new one to replace it. Copy/cut are removed from the key field's menu

## 0.12 — 2026-10-02

- **Secrets encrypted on the phone**: the node key, WireGuard keys, the server profiles with the SSH key and device keys are sealed with an AES-256-GCM key kept in the Android Keystore (it never leaves it); older plain values are re-encrypted on first use
- **No Android backups of app data** (cloud or device transfer): they used to copy these settings, the SSH key included, to Google Drive
- **Backup of settings** (Settings): everything in one file protected with your password (PBKDF2-SHA256, 310 000 rounds + AES-256-GCM) — for a new phone or a reinstall; restore shows the date and server and asks before replacing
- **Several servers**: Server → Servers — switch the active one (its own direct / wss peers go first), add another over SSH; importing a profile adds a server instead of replacing; setting a server up again keeps its wss wrapper and last good result
- **Server panel** (server card): system, CPU, memory, disk, network speed and totals, services, Yggdrasil peers, WireGuard devices online, pending updates and whether a reboot is needed — read-only over SSH, refreshed every 15 s; Update packages after a confirmation
- CI: the emulator test also checks that secrets are stored encrypted and that the old single server becomes the server list; Kotlin tests for the backup file

## 0.11 — 2026-10-02

- **Code cleanup**: the main screen is split into cards with their own views and dialogs (StatusCard, PeersCard, SettingsCard), the wss wrapper into WrapperUi, running server scripts into SetupRunner; the node status is parsed once per refresh (NodeStatus); shared view helpers and formatting in one place. No change in behaviour
- **CI starts the app on an Android emulator** after every build — fresh and with filled-in settings (dark theme, hidden addresses, a server with a wss wrapper); a crash fails the build. Kotlin unit tests for address hiding and profile links

## 0.10.4 — 2026-10-02

- Hide addresses: the grain moves at the same speed in every address — before, its speed was a share of the pill's width, so long addresses had much faster grain than short ones; now ±10 / ±3.5 dp per second everywhere, like the short pill that looked right

## 0.10.3 — 2026-10-02

- Hide addresses: a real Gaussian blur (three box-blur passes each way) instead of faint shifted copies — at large text sizes the random characters under the grain showed through; the grain now drifts across the pill and twinkles 2–3× faster

## 0.10.2 — 2026-10-02

- Connect / Disconnect: the animated edge is now a plain bolder line right on the button's edge, like the outline of the other buttons, without the blurred glow
- Hide addresses: the grain is alive, like Telegram's spoiler — specks twinkle and drift while the screen is visible (stopped in the background)
- **No status notification by default**: the Quick Settings tile does the same and Android keeps a running VPN alive by itself; it can be turned back on in Settings → Status notification
- **Background work** (Settings, and once on the first Connect): checks whether Android battery-optimises the app and offers to allow unrestricted background work (the system request, or the app's settings page for MIUI / HyperOS)

## 0.10.1 — 2026-10-02

- **Fixed: 0.10 crashed on start.** The status bar icons were coloured for the theme before the window existed (`getInsetsController()` with no decor view yet → NullPointerException)
- A crash is now saved and shown on the next start with Copy — to report it without adb
- Quick Settings tile: long-press opens the app; the tile's title shows the status (Disconnected / Connecting… / Via server) — some shades show only the title
- Peers: how long each link has been up (“up 12 min”)

## 0.10 — 2026-10-02

- **Themes** (Settings → Theme), as in AppShelf: As in the system, Light, Dark, Graphite, AMOLED, plus Transparency; applied at once, the window stays open; dialogs follow the theme
- **Animated edge on Connect / Disconnect**: a gradient running clockwise around the button with a soft glow — calm theme colours when off, amber and fast while connecting, green when connected; paused in the background
- **Hide addresses now covers them with a grainy blur** instead of example values: every address — yours and public ones, IPs, domains, peer URIs, the wss path, device names in Devices, the log — is drawn as a smear of a random string with grain, so nothing can be read from a screenshot; tapping still copies the real value
- **Auto-pick: choose how many peers to keep** (off, 2–6; 3 recommended), with the reasoning: one path carries the traffic, so more peers mean more battery and keepalive traffic, not more speed

## 0.9.2 — 2026-10-02

- **Reconnect after changes** (Settings, on by default): after editing peers, the all-traffic switch, auto-pick, app lists, the wrapper, or setting up / importing / removing the server, the running VPN reconnects by itself a second after the last change instead of asking you to
- Screenshots in README and for F-Droid (taken with Hide addresses)

## 0.9.1 — 2026-10-02

- **Hide addresses** (Settings): the server's host, IP and SSH port, the wss path and Yggdrasil addresses are shown as example values — for screenshots and showing the screen; only the display changes
- **Builds for more devices**: armeabi-v7a (32-bit phones) and x86_64 besides arm64 — the universal APK in the release; native libraries are 16 KB-aligned for Android 15+
- F-Droid preparations: fastlane descriptions (EN/RU), icon, changelogs, build recipe in docs/fdroid/

## 0.9 — 2026-10-02

- **Long-press help everywhere**, as in AppShelf: hold any button, switch or item (status, address, Connect, server and its details, wrapper, all-traffic switch, Set up again, Remove, Devices, peers and every peer row, Edit, Catalog, auto-pick, apps, always-on, log; in dialogs — SSH fields' labels, Set up automatically, device add / refresh / rows, app list modes, Share) to see what it does; a line in Settings tells about it. 27 bubbles in English and Russian

## 0.8.2 — 2026-10-02

- **The real cause of the wss drops was telemt, not Yggdrasil — correction to 0.8.1.** telemt on 443 relays non-MTProto traffic to the site with limits made for web pages: 5 s of silence, 60 s in total, 5 MB. Set up automatically now finds telemt and, with your permission, raises them in /etc/telemt/telemt.toml (5 min, a day, no byte cap; backup, restart, check). Checked on a real server: the wss link stays up 75 s+ (before: dropped every ~6 s, never lived past 60 s)
- 0.8.1 blamed Yggdrasil's own ws:// listener and put a WebSocket→TCP bridge in front of it; a test without proxies shows that listener is fine (stable 30 s+). The bridge is gone again: Set up automatically removes yggtunnel-wsbridge and switches Yggdrasil back to ws://127.0.0.1:21444; the wss address stays the same
- Opt-in Go test: `YGG_WSL_TEST=1 go test -run OwnWSListener` (Yggdrasil's ws listener without proxies)

## 0.8.1 — 2026-10-02

- **The wss wrapper no longer drops every few seconds.** (Wrong cause, see 0.8.2: it was telemt.) ~~Yggdrasil's own ws:// listener closes each link ~10 s after it opened~~. Now nginx proxies to a small WebSocket→TCP bridge (yggtunnel-wsbridge, Python standard library, a systemd service), which hands the stream to Yggdrasil's tcp://127.0.0.1:21445. Run Set up automatically once: a server with the old setup is moved over, the wss address stays the same. Tested: the link stays up (was dropping every 6.5 s), the bridge moves 400+ Mbit/s
- Dialogs no longer jump in from the side: they are styled before their first frame
- Opt-in Go tests against a real peer / the bridge: `YGG_WATCH_PEER=… go test -run WatchPeer`, `YGG_BRIDGE_TEST=1 go test -run Bridge`

## 0.8 — 2026-10-02

- **The wss wrapper sets itself up**: Server → Wrapper through the site → Set up automatically. The app finds the HTTPS site in nginx — on 443 itself or behind telemt / xray / haproxy on 443 — and adds a secret WebSocket path to Yggdrasil's local listener; with several sites it asks which one. On a server with free 443 and 80 it asks for a domain (a free deSEC / DuckDNS one is fine), installs nginx and certbot, gets a Let's Encrypt certificate and makes a small site with the wrapper
- Safe by design: every changed file is backed up to /var/backups/yggtunnel/, nginx -t and a WebSocket handshake through the public address are checked, any failure restores everything; an unknown program on 443 is left alone with an explanation. Running again keeps the same path and changes nothing; only what changed is reloaded
- Remove wrapper takes it off the server again (or Forget only removes it from the app)

## 0.7.1 — 2026-10-02

- Wrapper through the site: a wss address without a port gets :443 added (Yggdrasil refuses it otherwise: “missing port in address”). Checked from outside: the wss peer through the site comes up in half a second
- An opt-in Go test against a real peer: `YGG_TEST_PEER=wss://host:443/path go test -run RealPeer .`

## 0.7 — 2026-10-02

- **Wrapper through the site (wss)** in the server card: Yggdrasil peering inside HTTPS to your own site on the server (WebSocket behind its web server), so to the network it is ordinary traffic to the site with its real certificate; the peer is pinned (auto-pick never parks it), stays first after catalog refreshes and goes into device profiles
- Auto-pick keeps all of the server's own peers (direct and wss), not just one
- Set up again keeps local WebSocket listeners (ws://127.0.0.1:…) in the server's Yggdrasil config

## 0.6 — 2026-10-02

- **Always-on VPN**: YggTunnel supports Android's always-on VPN (starts after a reboot or a crash) and “Block connections without VPN” as a kill switch; Settings → Always-on VPN explains it and opens the system page
- **Quick Settings tile**: connect / disconnect with one tap, shows the state
- **Status notification** with Disconnect while connected (also keeps the service in the foreground, so aggressive firmware kills it less)
- **Peers from the catalog** (publicpeers.neilalexander.dev): Peers → Catalog takes reliable peers (up ≥ 90% of recent checks), times them from the phone and keeps the fastest TLS/WSS plus QUIC on the same hosts; an automatic list refreshes itself weekly, a list edited by hand is left alone
- **The server picks its own peers**: on setup it times the catalog's reliable peers from the server itself and takes the 4 fastest (before, it used the phone's peers even for a server far away from it)
- “Only through VPN” no longer wraps in its button

## 0.5.1 — 2026-10-02

- **Profiles are kept on the server**: the devices' private keys are stored there (root-only file), so the QR code / link of any device can be shown again from any admin phone; this phone's key and the profiles kept on the phone by 0.5 are moved to the server automatically when Devices opens. The private key is checked against the device before it is saved
- Script variables (device keys among them) go to the server inside the script on stdin, no longer on the SSH command line, which other users of the server could see (ps)

## 0.5 — 2026-10-02

- **Devices** (server card): every device on the server with its name, address, when it was last online and its traffic; add (with a name), rename, remove (disconnects at once, no restarts), show the QR code / link again, copy the public key. “This phone” and devices whose profile is saved here are marked. The profile can be shown again only for devices added from this phone — private keys never go to the server
- This phone gets its model as the device name on server setup
- **Apps and the VPN**: two modes — “Bypass the VPN” (the checked apps go around it) and “Only through VPN” (only the checked apps use it), each with its own list

## 0.4 — 2026-10-02

- **Peer auto-pick** (Settings, on by default): all listed peers are dialed, then only the 3 fastest stay connected (plus the server's own peer while it is reachable); the rest wait in reserve and are dialed again if fewer than 2 peers are left — fewer idle connections, less battery
- **Apps that bypass the VPN** (Settings): launcher apps with search and checkboxes — e.g. banks and local services use the regular internet
- **Add device** (server card): the app registers one more WireGuard key on the server over SSH (no reinstall, no restarts) and shows a QR code / link `yggtunnel://import#…`; on the other phone it opens in YggTunnel (or paste it via Import profile) — that phone gets the full tunnel without SSH access
- The peer list also shows links the node has that are not in the list (e.g. a removed peer until reconnect) and peers in reserve

## 0.3 — 2026-10-02

- **Full tunnel through your server**: with a set-up server all traffic goes through WireGuard to it, and WireGuard itself travels inside Yggdrasil — so the phone never connects to the server's IP directly (unless it is reachable, then it is just the shortest path)
- Yggdrasil addresses still go straight into Yggdrasil, not through the server
- When the server has no IPv6 internet, IPv6 requests are answered “unreachable” at once: apps switch to IPv4 immediately and nothing leaks past the tunnel
- Status: “Connected via server”, time since the last WireGuard handshake and tunnel traffic; switch “All traffic through the server” in the server card (off — Yggdrasil only, as before)
- WireGuard runs inside the Go core (wireguard-go) with its UDP built and parsed in-process — no extra network stack; covered by an end-to-end test: two Yggdrasil nodes, a packet goes phone → server and back
- “Set up again” no longer wraps and gets cut in the button

## 0.2.4 — 2026-10-02

- Server setup: WireGuard failed to start on Ubuntu 26.04 — its AppArmor profile does not let wg-quick run the firewall script (PostUp, “Permission denied”); the firewall is now a separate service, yggtunnel-fw
- The server details are saved as soon as you tap Set up, also when the setup fails; the card shows “Not set up yet” with Set up and Remove
- The setup log and the node log take at most 60% of the screen, so the Close / Copy buttons no longer disappear under a long log

## 0.2.3 — 2026-10-02

- The server setup fixes of 0.2.1 and 0.2.2 actually reach the app now: the build did not rebuild the native library when only the server script changed, so both versions still carried the 0.2 script

## 0.2.2 — 2026-10-02

- Server setup: fixed a random failure with code 141 (SIGPIPE) right after installing packages

## 0.2.1 — 2026-10-02

- Server setup: if WireGuard fails to start, the log now shows why (the service journal); checks that the kernel has WireGuard and switches IPv6 on if the VPS has it off (Yggdrasil needs it); no more needrestart noise

## 0.2 — 2026-10-02

- **Server setup from the app**, like AmneziaVPN: enter the IP, SSH port, user and private key (or pick the key file) — the app logs in over SSH and installs and configures Yggdrasil and WireGuard on Ubuntu/Debian with a live log
- On the server: NAT for the phone's WireGuard, on the Yggdrasil interface only WireGuard, SSH and ping are open; the server also listens for Yggdrasil peers itself (TLS and QUIC, port 21443) and is added first to the phone's peer list
- The phone's WireGuard key is created on the phone; only the public key goes to the server. The server's SSH key is remembered on first connect and checked afterwards
- SSH runs in the Go core (golang.org/x/crypto/ssh): ed25519, RSA, ECDSA keys, with or without a passphrase

## 0.1.1 — 2026-10-02

- Fixed: peers dropped a few seconds after connecting, and other apps lost IPv4 while connected. The app's own connections now bypass the VPN, and IPv4 is no longer blocked (only Yggdrasil 200::/7 goes into the tunnel)
- Latency shows “—” until it is measured instead of “0 ms”

## 0.1 — 2026-10-02

- First version: a Yggdrasil client (yggdrasil-go 0.5.14). Connect / Disconnect, your Yggdrasil address (tap to copy),
  peers with latency and traffic, editable peer list (default: nearby public peers over TLS, QUIC and WSS), node log
- The address stays the same between runs (the node key is generated once)
- Peers are redialed at once when the network changes (Wi-Fi ↔ mobile)
