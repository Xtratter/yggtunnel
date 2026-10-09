# Linux client, plan 4: split routing by subnet and domain

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The user can choose which destinations use the tunnel: everything (today's behaviour), everything except a list, or only a list. The list holds subnets and domain names. Works from the window and from `yggtunnelctl`.

**Architecture:** The existing scheme (`ip rule` + a table whose default route is the tunnel + an nftables `route` chain that marks packets) gets a mode. Mode `exclude` keeps «everything through the tunnel» and marks the listed destinations with the existing bypass mark `0x5967`. Mode `only` flips the default: unmarked traffic goes direct and listed destinations get a new mark `0x5968` that selects the tunnel table. Subnets are static rules; domains are resolved by the daemon into nftables sets that it refreshes while connected. Changing the **mode** needs a reconnect; changing the **lists** is applied live.

**Tech Stack:** Go (`vishvananda/netlink`, `google/nftables`, `golang.org/x/net/idna`), Qt 6 QML.

**Spec:** `docs/superpowers/specs/2026-10-09-linux-client-design.md` (section «Split routing», subnets/domains part). Builds on plans 1–3. Routing **by application** (cgroup) is plan 5.

## Global Constraints

- Neutral wording everywhere (code, UI strings, docs, commits): describe the feature as «choose which destinations use the tunnel»; no mention of blocking, censorship, operators, throttling, «masking».
- No real IPs, ports, domains or keys; examples only (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`, `example.com`, `example.net`, `example.org`, test LAN `192.168.77.0/24`).
- Modes (string): `all` (default, lists ignored), `exclude`, `only`. Marks: `0x5967` = bypass the tunnel (exists), `0x5968` = force the tunnel (new). Routing table and rule priorities as today (table 51871; priorities 32763 suppress, 32764 tunnel).
- `exclude`: today's rules; the `mark` chain additionally sets `0x5967` on packets whose destination is in the lists.
- `only`: no suppress rule and no catch-all; one rule `fwmark 0x5968 lookup 51871` (priority 32764, both families); the `mark` chain sets `0x5968` on packets to listed destinations **only when the packet is not already marked `0x5967`** (the daemon's own traffic must never be pulled into the tunnel); the DNS servers (`1.1.1.1`, `8.8.8.8`) are always in the forced set so DNS keeps working.
- Kill switch in `only` mode: it protects only what was meant to be tunnelled — a single rule `meta mark 0x5968 oifname != "<tun>" counter drop` (comment `yggtunnel-ks-drop`), everything else is accepted. In `all`/`exclude` the existing rules stay.
- DNS in `only` mode: servers set on the tunnel link as today, but `DefaultRoute=false` and routing domains `~<domain>` for each listed domain (so only those names are asked through the tunnel). In `all`/`exclude` unchanged (`~.`, default route).
- Validation (shared, `internal/splitcfg`): at most 500 subnets and 500 domains; subnets are CIDR or a bare address (→ `/32` or `/128`), normalised with `Masked()`; reject `0.0.0.0/0`, `::/0`, anything that overlaps `200::/7` (the tunnel's own address space) and the loopback ranges; domains lowercased, trailing dot removed, IDN converted to ASCII (punycode), labels `[a-z0-9-]` 1–63 characters, total ≤ 253, no wildcards, at least two labels (no bare TLDs). Subdomains are **not** included automatically — list each name. Duplicates collapse.
- Domain resolution: the system resolver, every 60 s while connected and immediately when the list changes; each answer's addresses (A and AAAA) are kept in the sets for 5 minutes (nftables element timeout); a failed lookup keeps the old elements and is reported in the status; never more than 8 concurrent lookups; a lookup that takes over 5 s is abandoned.
- Protocol: `set` gains `"split": {"mode": string, "subnets": [string], "domains": [string]}` (replaces the whole split setting; validated; polkit-authorised like the other settings). Changing `mode` while `Starting`/`Connected`/`Reconnecting` is refused with the error text «disconnect first to change the routing mode». `status` gains `settings.split` (normalised) and `splitStatus: {"mode": "all|exclude|only" (applied), "resolved": int (addresses currently in the sets), "resolveError": string?, "resolvedAt": RFC3339?}`.
- Defaults: mode `all`, empty lists. A damaged settings file gives the defaults.
- Network tests run only inside `nstest.InNetns`; nothing on the real host network changes before the user confirms.
- Docs bilingual (EN + RU); commits in English, author `Xtratter <1359019+Xtratter@users.noreply.github.com>`, trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Nothing is pushed or released without the user's explicit word.

## Review Focus

- `only` mode: unlisted traffic goes direct, listed traffic goes through the tunnel; the daemon's own traffic to a listed address still goes direct (no loop). Task 1.
- `exclude` mode: listed destinations go direct, everything else through the tunnel; a listed address is also exempt from the kill switch drop. Task 1 and 2.
- Mode `all` is byte-for-byte today's behaviour (the existing snapshot tests stay green). Task 1.
- Rollback and crash recovery remove the sets, rules and the new ip rules in every mode. Task 1.
- Kill switch with `only`: tunnel route vanishes → marked traffic is dropped; unmarked traffic keeps flowing. Task 2.
- Hostile or sloppy input: `0.0.0.0/0`, `200::/7` overlaps, 100 000 entries, uppercase/trailing-dot/IDN names, a name with spaces or `*`, duplicate entries, IPv6 zone IDs. Task 4.
- Domain refresh: lookup failure keeps the old addresses; a list change resolves at once; goroutines stop on `down`; no refresh traffic is mis-marked. Task 4.
- Changing the mode while connected is refused and changes nothing; changing the lists while connected applies without dropping the connection. Task 4.
- The window never offers a mode change while connected; long lists and error text do not break the layout. Task 6.

---

### Task 1: Modes in the routing scheme

**Files:**
- Modify: `linux/internal/netconf/link.go` (`Params`), `route.go` (`addRules`), `mark.go` (`addMark`), `configure.go`.
- Create: `linux/internal/netconf/split.go`, `linux/internal/netconf/split_test.go`.

**Interfaces — Produces:**
```go
type SplitParams struct { Mode string; Subnets []netip.Prefix }   // Mode "all" | "exclude" | "only"; Subnets already normalised
// Params gains:  Split SplitParams;  ForceMark uint32 (0x5968)
func UpdateNames(addrs []netip.Addr) error   // replaces the contents of sets names4/names6 in table inet yggtunnel in ONE batch; element timeout 5 min; no-op error if the table is missing
func UpdateSubnets(subnets []netip.Prefix) error   // replaces the static subnet rules/sets in one batch (live list change)
const ForceMark = 0x5968
```
Behaviour: `Configure` creates, inside the existing table `inet yggtunnel`, the sets `names4`/`names6` (`ipv4_addr`/`ipv6_addr`, timeout flag) and the subnet matches; in `exclude` the `mark` chain gets `ip daddr @names4 / subnets → meta mark set 0x5967` (and the IPv6 equivalents); in `only` it gets `meta mark != 0x5967` guarded rules that set `0x5968`, and the ip rules differ as described in Global Constraints. Step kinds stay `rule`/`nft` (the table is removed as a whole by the existing `delMarkTable`).

- [ ] **Step 1: Write the failing tests** (`split_test.go`, namespace helper like `ksEnv`: dummy link `lan` `192.168.77.2/24` default via `.1`; counters from a test table `inet yggtest` at `postrouting` priority 200 with `oifname "lan" ... counter comment "direct"` and `oifname "yggtun0" ... counter comment "tunnel"` per destination; senders use plain `net.Dial("udp", …)`; the process under test is NOT in the marked cgroup unless stated):
  - `TestModeAllUnchanged` — mode `all`: a send to `203.0.113.9` counts «tunnel»; existing `TestConfigureThenRollbackRestoresRoutes` stays green.
  - `TestExcludeSubnetGoesDirect` — mode `exclude`, subnet `203.0.113.0/24`: send to `203.0.113.9` counts «direct», send to `198.51.100.9` counts «tunnel».
  - `TestExcludeIPv6SubnetGoesDirect` — `2001:db8:77::/48` direct, `2001:db8:99::1` tunnel (a `fd00::`/`2001:db8::` address on `lan` plus a v6 default route in the namespace).
  - `TestExcludeNamesGoDirect` — `UpdateNames([192.0.2.55])`, send to it: «direct»; `UpdateNames(nil)`: «tunnel» again.
  - `TestOnlySubnetGoesThroughTunnel` — mode `only`, subnet `203.0.113.0/24`: `203.0.113.9` «tunnel», `198.51.100.9` «direct».
  - `TestOnlyNamesGoThroughTunnel` — as above with `UpdateNames`.
  - `TestOnlyDaemonTrafficStaysDirect` — mode `only`, the daemon's own cgroup (the test process's), listed destination `203.0.113.9`: counts «direct» (the `meta mark != 0x5967` guard).
  - `TestOnlyDNSServersAreForced` — mode `only`, empty lists: a send to `1.1.1.1` counts «tunnel».
  - `TestOnlyHasNoSuppressRule` — `ip rule show` in mode `only` has `fwmark 0x5968 lookup 51871` and no `suppress_prefixlength`.
  - `TestSplitRollbackAndRecoverLeaveNoTrace` — for each mode: `nstest.Snapshot` equality after `Rollback` and after `RecoverFrom` of the persisted record.
  - `TestUpdateNamesIsAtomicAndBounded` — 3 000 addresses in one call succeed; with `ksFlush`-style injected failure (`splitFlush` variable) the old elements stay.
  - `TestUpdateSubnetsLive` — change the subnet list while up; behaviour follows at once.
- [ ] **Step 2:** `cd linux && go test ./internal/netconf -run 'Split|Exclude|Only|ModeAll|UpdateNames|UpdateSubnets'` → FAIL.
- [ ] **Step 3: Implement.** Per-prefix rules are acceptable instead of interval sets (≤ 500 entries). Replace sets/rules with the add-delete-add batch idiom already used by `applyKillSwitch`, behind an overridable `splitFlush` like `ksFlush`.
- [ ] **Step 4:** whole `netconf` package passes with `-race`; `go vet ./...`. **Commit** `feat(linux): split routing modes in the routing scheme`.

### Task 2: Kill switch and DNS in `only` mode

**Files:** Modify `linux/internal/netconf/killswitch.go`, `killswitch_test.go`, `linux/internal/dns/resolved.go`, `resolved_test.go`.

**Interfaces — Produces:**
```go
// KSParams gains:  Mode string   // "" or "all"/"exclude": existing rules; "only": the single marked-traffic rule
// dns.Options replaces the plain call:
type Options struct { DefaultRoute bool; Domains []string }   // DefaultRoute true => "~." as today
func SetWith(c Conn, ifIndex int, servers []netip.Addr, o Options) error
// dns.Set(c, i, s) stays and means SetWith(..., Options{DefaultRoute: true})
```

- [ ] **Step 1: Tests:** netconf — `TestKillSwitchOnlyModeDropsMarkedTrafficWhenTunnelRouteVanishes` (mode `only`, listed destination, `ip route flush table 51871` → counter 1), `TestKillSwitchOnlyModeLeavesUnmarkedTrafficAlone` (unlisted destination goes direct: counter 0), `TestKillSwitchExcludeListedDestinationIsNotDropped` (mode `exclude`, listed destination goes direct and is accepted through the mark rule). dns — `TestSetWithDomainsAndNoDefaultRoute` (fake `Conn`: `SetLinkDomains` gets `~example.com`-style entries with `RoutingOnly=true` and `SetLinkDefaultRoute` gets `false`), `TestSetWithDefaultRouteKeepsTildeDot`, `TestSetWithEmptyDomainsAndNoDefaultRoute` (no `SetLinkDomains` entries, default route false).
- [ ] **Step 2:** FAIL; **Step 3:** implement; **Step 4:** both packages pass with `-race`. **Commit** `feat(linux): kill switch and DNS follow the split mode`.

### Task 3: Validation package

**Files:** Create `linux/internal/splitcfg/splitcfg.go`, `splitcfg_test.go`.

**Interfaces — Produces:**
```go
type Config struct { Mode string `json:"mode"`; Subnets []string `json:"subnets"`; Domains []string `json:"domains"` }
type Normalized struct { Mode string; Subnets []netip.Prefix; Domains []string }
func Normalize(c Config) (Normalized, Config, error)   // the Config returned is the canonical text form (what is stored and shown)
const MaxEntries = 500
```
Empty `Mode` means `all`. Unknown mode is an error.

- [ ] **Step 1: Tests** (table-driven): valid cases (CIDR, bare v4/v6, uppercase and trailing-dot domains normalised, `Bücher.example` → punycode, duplicates collapsed, order preserved) and rejects with readable errors (`0.0.0.0/0`, `::/0`, `200::/7`, `200:db8::/32`, `127.0.0.0/8`, `::1`, `256.1.1.1`, `1.2.3.4/33`, `fe80::1%eth0`, `*.example.com`, `exa mple.com`, `example..com`, `-bad.example`, a 64-letter label, a 254-character name, bare `com`, 501 subnets, 501 domains, unknown mode). `TestNormalizeNeverPanics` with random byte strings (fixed seed, 5 000 inputs).
- [ ] **Step 2:** FAIL; **Step 3:** implement (`netip.ParsePrefix`, `idna.Lookup.ToASCII`); **Step 4:** PASS. **Commit** `feat(linux): split routing list validation`.

### Task 4: Settings, resolver loop and daemon behaviour

**Files:** Modify `linux/internal/store/store.go` (+tests), `linux/internal/daemon/daemon.go`, `state.go`, `real.go`, `daemon_test.go`, `integration_test.go`; create `linux/internal/daemon/splitwatch.go`, `splitwatch_test.go`.

**Interfaces:**
- Produces: `store.Settings` gains `Split splitcfg.Config` (default mode `all`); `daemon.Net` gains `SplitNames(addrs []netip.Addr) error` and `SplitSubnets(s []netip.Prefix) error` (real: `netconf.UpdateNames/UpdateSubnets`); `daemon.Status` gains `SplitStatus` as in Global Constraints; the resolver loop:
```go
type splitWatcher struct { /* injected: lookup func(ctx, host string) ([]netip.Addr, error), apply func([]netip.Addr) error, now func() time.Time, interval time.Duration */ }
func (w *splitWatcher) Start(domains []string); func (w *splitWatcher) SetDomains(domains []string); func (w *splitWatcher) Stop()
func (w *splitWatcher) Status() (resolved int, at time.Time, err error)
```
- `up` passes `Split` into `netconf.Params` and `KSParams.Mode`, `dns.Options` for the mode, starts the watcher after the tunnel is attached when the mode is not `all` and there are domains; `down`/teardown stop it.
- `set` with `split`: normalise; mode change while not `Off` → error «disconnect first to change the routing mode»; list change while connected → `SplitSubnets`, `watcher.SetDomains` (apply first, store on success, as for the kill switch); while `Off` only stored.

- [ ] **Step 1: Tests (fakes)** — watcher: `TestWatcherResolvesImmediatelyAndPeriodically` (fake clock/ticker), `TestWatcherLookupFailureKeepsOldAddressesAndReportsError`, `TestWatcherSetDomainsResolvesAtOnce`, `TestWatcherStopStopsGoroutines` (leak check with `runtime.NumGoroutine` or a done channel), `TestWatcherLimitsConcurrentLookups` (never more than 8 in flight with 40 domains), `TestWatcherAbandonsSlowLookup`, `TestWatcherDeduplicatesAddresses`. Daemon — `TestSplitDefaultsInStatus`, `TestSetSplitWhileOffStores`, `TestSetSplitRejectsInvalid` (state and store unchanged), `TestSetSplitModeChangeRefusedWhileConnected`, `TestSetSplitListChangeAppliesLive` (fake records `net.subnets`, watcher domains updated), `TestUpPassesSplitToNet` (params mode/subnets seen by `fakeNet.Up`; kill-switch params carry the mode; DNS options), `TestUpStartsWatcherOnlyWhenNeeded`, `TestDownStopsWatcher`, `TestSplitSetRefusedWhenAuthorizerDenies`, `TestSplitStatusReportsResolvedCount`. Store — defaults, round trip, damaged file → defaults, an old `settings.json` without `split` loads with mode `all`. Integration (namespace) — `TestIntegrationSplitOnlyThroughDaemon` (`set` mode only with a subnet, `up`, listed destination counts «tunnel», unlisted «direct», `down` → snapshot equals initial).
- [ ] **Step 2:** FAIL; **Step 3:** implement; **Step 4:** `go vet ./... && go test -race ./...` in `linux/`. **Commit** `feat(linux): split routing settings, resolver loop and daemon integration`.

### Task 5: Command line

**Files:** Modify `linux/cli/main.go`, `linux/cli/cli_test.go`.

Commands: `yggtunnelctl split mode all|exclude|only`, `split add <subnet|domain>`, `split remove <subnet|domain>`, `split show`; `status` prints `Routing: all traffic | all except the list | only the list (N subnets, M domains; K addresses resolved)` and the resolver error if any. `add`/`remove` read the current lists from `status`, change them and send the whole `split` object in one `set`; an entry containing `/` or parseable as an IP is a subnet, anything else a domain (the daemon validates).

- [ ] **Step 1: Tests** with the stub handler: `TestCLISplitModeSendsSet`, `TestCLISplitAddSubnetKeepsExisting`, `TestCLISplitAddDomain`, `TestCLISplitRemove`, `TestCLISplitRemoveMissingIsNoop` (no `set` sent), `TestCLISplitBadArguments` (exit 2), `TestCLISplitShowListsEntries`, `TestCLIStatusShowsRouting` (three modes), `TestCLIDaemonErrorForModeChangeIsPrinted`.
- [ ] **Step 2:** FAIL; **Step 3:** implement; **Step 4:** PASS. **Commit** `feat(linux): split commands`.

### Task 6: Window: the routing card

**Files:** Create `linux/gui/qml/RoutingCard.qml`; modify `linux/gui/src/controller.h/.cpp`, `qml/Main.qml`, `CMakeLists.txt` (resource list), `tests/tst_controller.cpp`, `tests/tst_window.cpp`, `translations/yggtunnel_ru.ts`.

**Interfaces — Produces** (on `ctl`): `splitMode` (string), `splitSubnets` (QStringList), `splitDomains` (QStringList), `splitResolved` (int), `splitResolveError` (string), `splitApplied` (string, the mode the daemon applied); `Q_INVOKABLE void setSplit(const QString &mode, const QStringList &subnets, const QStringList &domains)` (sends `set` with `split`; disabled while `busy`).

Card: title «Routing»; three radio buttons «All traffic through the tunnel», «All traffic except the list», «Only the list through the tunnel» (disabled while connected, with the line «Disconnect to change this»); two multi-line fields «Subnets» and «Domains» (one entry per line, shown only when the mode is not «all»); «Apply» (sends the lists; the mode too when disconnected); the daemon's error is shown under the button; a status line «N addresses resolved» / the resolver error while connected.

- [ ] **Step 1: Tests:** controller — `splitFieldsAreReadFromStatus`, `setSplitSendsOneSetWithTheWholeObject`, `setSplitIgnoredWhileBusy`, `splitErrorIsShown`; window — `routingCardShowsModesAndFields`, `modeRadiosDisabledWhileConnected`, `fieldsHiddenInModeAll`, `applySendsSplit` (fake receives the object with the typed lines split and trimmed, empty lines dropped), `longListDoesNotBreakLayout` (300 lines, the card keeps its width), `routingCardHiddenWhenUnreachable`, `screenshotRouting` (view the PNG, fix layout faults); the `translations` CTest stays green with Russian strings.
- [ ] **Step 2:** FAIL; **Step 3:** implement; **Step 4:** `ctest --test-dir linux/gui/build` passes, screenshots read. **Commit** `feat(gui): routing card`.

### Task 7: Docs and version

**Files:** Modify `linux/README.md`, `linux/README.ru.md`, `linux/CHANGELOG.md`, `linux/CHANGELOG.ru.md`, `linux/internal/version/VERSION` (`0.4.0`).

- [ ] **Step 1:** Document: the three modes, the lists, the commands, validation rules, that subdomains are not included, how domains are resolved (system resolver, 60 s, 5-minute entries; CDN names may change addresses between lookups), that the mode is changed only while disconnected, the interplay with the kill switch and DNS, how to check by hand (`ip rule`, `sudo nft list table inet yggtunnel`), limits (answers of a name can differ between resolvers; no application routing yet). Remove «split routing by subnet/domain» from «Not yet» (applications stay).
- [ ] **Step 2:** word/IP scan of the diff, `go vet ./... && go test ./...` in `linux/`, `ctest` in `linux/gui/build`. **Commit** `docs(linux): split routing, 0.4.0`.
