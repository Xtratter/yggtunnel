# Linux client, plan 3: kill switch

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While the tunnel is up, traffic that is not going through it (and is not the daemon's own, a local-network exception or DHCP) is dropped, so a lost tunnel route can never leak packets onto the physical link. Switchable from the window and from `yggtunnelctl`.

**Architecture:** A second nftables table `inet yggtunnelks` with one `output` filter chain: accept rules for what must keep working, then a counted `drop`. It is a step in the same transaction (`netconf.Tx`) as the rest of the tunnel, so rollback, `down`, `panic` and crash recovery remove it with everything else. The setting lives in the store; the daemon applies it on `up`, on change while connected, and removes it on `down`.

**Tech Stack:** Go (`google/nftables`, no `nft` binary at runtime), Qt 6 QML for the toggles.

**Spec:** `docs/superpowers/specs/2026-10-09-linux-client-design.md` (section «Kill switch»). Builds on plans 1 and 2 (daemon, window).

## Global Constraints

- Neutral wording everywhere (code, UI strings, docs, commits): no mention of blocking, censorship, operators, throttling, «masking». Describe the feature as «kill switch: traffic that is not going through the tunnel is dropped».
- No real IPs, ports, Ygg addresses, domains or keys; examples only (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`, private LAN examples `192.168.77.0/24`).
- Table name `yggtunnelks` (family `inet`), chain `output` (type `filter`, hook `output`, priority `filter` = 0), step kind `killswitch`; drop rule carries the comment `yggtunnel-ks-drop` and a counter.
- Accept rules, in this order: `oifname "lo"`; `oifname "<tunnel if>"`; `meta mark <mark>` (the daemon's own cgroup, marked by the existing `inet yggtunnel` chain); DHCP (`udp sport 68 dport 67`, `udp sport 546 dport 547`); neighbour discovery (`icmpv6` router-solicit, neighbour-solicit, neighbour-advert); when `allowLAN`: destinations `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `224.0.0.0/4`, `fe80::/10`, `fc00::/7`, `ff00::/8`; then `counter drop`.
- Defaults: kill switch **off**, local network **allowed** (when the kill switch is on).
- Fail-open on purpose: a crashed or killed daemon must never lock the user out of the network — `ExecStopPost` (`yggtunneld --recover-only`) removes the table. This is documented, not hidden.
- Not covered (documented limits): forwarded traffic (containers, VM bridges), traffic of the window before `up`.
- Protocol additions (daemon ↔ clients): command `set` with args `{"killSwitch":bool?, "allowLan":bool?}` (state-changing: polkit-authorised like `up`); `status` gains `settings:{killSwitch,allowLan}` and `killSwitchActive:bool`.
- Docs bilingual (EN + RU); commits in English, author `Xtratter <1359019+Xtratter@users.noreply.github.com>`, trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- Network tests run only inside `nstest.InNetns` (throw-away user+network namespace). Nothing on the real host network is changed before the user confirms.
- Nothing is pushed or released without the user's explicit word.

## Review Focus

- Tunnel route vanishes while the kill switch is on: unmarked traffic to the physical link is dropped and counted. Task 1.
- The daemon's own traffic (peers, server) keeps flowing with the kill switch on. Task 1.
- `allowLan=false` drops LAN destinations; `allowLan=true` still drops public destinations sent via the LAN. Task 1.
- DHCP renewals and IPv6 neighbour discovery survive `allowLan=false`. Task 1.
- Every exit path removes the table: `down`, failed `up`, `panic`, crash recovery, toggling off while connected, daemon `Shutdown`. Task 1 and 2.
- Toggling on while `Off` arms nothing (it only stores the setting); toggling while `Starting` is refused or applied once the tunnel is up. Task 2.
- A second «on» is idempotent; a stale table from an earlier run does not make `up` fail. Task 1.
- A client without polkit authorisation cannot change the setting. Task 2.
- The window never shows the kill switch as active unless the daemon says so. Task 4.

---

### Task 1: The firewall step

**Files:**
- Modify: `linux/internal/netconf/tx.go` (add `Undo(kind string) error`; add `case "killswitch"` to `undoStep`).
- Create: `linux/internal/netconf/killswitch.go`, `linux/internal/netconf/killswitch_test.go`.

**Interfaces — Produces:**
```go
type KSParams struct { IfName string; Mark uint32; AllowLAN bool }
func AddKillSwitch(tx *Tx, p KSParams) error      // step{Kind:"killswitch"}; replaces a stale table instead of failing
func (t *Tx) Undo(kind string) error              // runs and forgets the undo of the LAST step of that kind, re-records; nil if none
func KillSwitchActive() bool                      // the table exists (used by status)
```

- [ ] **Step 1: Write the failing tests** (`killswitch_test.go`, all via `nstest.InNetns`; helper builds a namespace with a dummy link `lan` = `192.168.77.2/24`, default via `192.168.77.1`, runs `Configure` with the cgroup of the test process for the exempt case and the root cgroup `/sys/fs/cgroup` for the "other application" case, then `AddKillSwitch`; drop packets are read from `nft list table inet yggtunnelks` as `comment "yggtunnel-ks-drop"` counter `packets N`; leaks are produced by `ip route flush table 51871`, which sends unmarked traffic to the main table's default route via `lan`):
  - `TestKillSwitchDropsLeakWhenTunnelRouteVanishes` — other cgroup, flush the tunnel table, UDP to `203.0.113.9:9`: drop counter 1.
  - `TestKillSwitchLetsDaemonTrafficOut` — own cgroup (marked), same flush, same send: drop counter 0.
  - `TestKillSwitchLANBlockedWhenNotAllowed` and `TestKillSwitchLANAllowed` — send to `192.168.77.50:9`: counter 1 with `AllowLAN=false`, 0 with `true`.
  - `TestKillSwitchPublicViaLANStillDroppedWhenLANAllowed` — `AllowLAN=true`, send to `203.0.113.9:9` via the leak: counter 1.
  - `TestKillSwitchLetsDHCPOut` — `AllowLAN=false`, UDP from port 68 to `192.168.77.1:67`: counter 0.
  - `TestKillSwitchLetsTunnelTrafficOut` — leave the tunnel route in place; UDP to `203.0.113.9:9` from the other cgroup goes out of `yggtun0`: counter 0.
  - `TestKillSwitchRollbackRemovesTable` and `TestKillSwitchRecoverFromRemovesTable` — `nstest.Snapshot` equality, as in plan 1.
  - `TestTxUndoRemovesOnlyThatStep` — pure: steps a, killswitch, b; `Undo("killswitch")` runs only that undo and the record keeps a and b; calling it again returns nil.
  - `TestKillSwitchTwiceIsIdempotent` — two `AddKillSwitch` calls leave one table; a pre-existing table of that name does not make it fail.
  - `TestKillSwitchActiveReflectsTable`.
- [ ] **Step 2:** `cd linux && go test ./internal/netconf -run 'KillSwitch|TxUndo'` → FAIL (undefined).
- [ ] **Step 3: Implement** with `google/nftables` (accept rules as `expr` lists; prefix matches via `Payload`+`Bitwise`+`Cmp` on `meta nfproto`-scoped address offsets, or interval sets if the library supports them cleanly — one rule per prefix is acceptable). Create the table with `Flush` of any stale table of the same name first. `Undo` reuses the step's recorded `undo` func and re-calls `rec`.
- [ ] **Step 4:** whole `netconf` package passes, `go vet ./...`. **Commit** `feat(linux): kill switch firewall step`.

### Task 2: Settings and daemon behaviour

**Files:**
- Modify: `linux/internal/store/store.go` (settings), `linux/internal/daemon/daemon.go`, `state.go` (status fields), `real.go` (adapters), `linux/internal/daemon/daemon_test.go`, `integration_test.go`.

**Interfaces:**
- Consumes: Task 1.
- Produces:
```go
// store
type Settings struct { KillSwitch bool `json:"killSwitch"`; AllowLAN bool `json:"allowLan"` }
func (s *Store) Settings() Settings                 // defaults: KillSwitch false, AllowLAN true; a damaged file gives the defaults
func (s *Store) SaveSettings(v Settings) error
// daemon.Net gains:
KillSwitch(tx *netconf.Tx, on bool, p netconf.KSParams) error   // on: AddKillSwitch; off: tx.Undo("killswitch")
// Status gains:
Settings store.Settings `json:"settings"`; KillSwitchActive bool `json:"killSwitchActive"`
```
Behaviour: `set` stores the settings; if `Connected` it applies the change at once (add/remove/replace for a changed `allowLan`); if `Off` it only stores; if `Starting`/`Reconnecting` it stores and the running `up` applies the final value after the tunnel is attached (re-read just before applying). `up` arms the kill switch after `AttachTunnel` when enabled, and a failure there rolls the whole `up` back. `down`/`panic`/`Shutdown`/failed `up` remove it through the transaction (no extra code path). `set` needs the polkit action like `up`.

- [ ] **Step 1: Failing tests** (fakes, `daemon_test.go`): `TestSettingsDefaults`; `TestSettingsPersistAndSurviveDamagedFile`; `TestSetWhileOffOnlyStores` (no `Net.KillSwitch` call); `TestUpArmsKillSwitchAfterAttach` (call order `core.start,net.up,core.attach,net.killswitch`); `TestUpWithoutKillSwitchDoesNotArm`; `TestSetWhileConnectedAppliesAtOnce` (on, off, allowLan change → on again with the new parameter); `TestKillSwitchFailureRollsUpBack` (fake `KillSwitch` errors → `undo…,core.stop`, state `off`); `TestDownRemovesKillSwitch` (via the transaction's undo, which the fake records); `TestSetDuringStartingAppliesAfterAttach`; `TestStatusReportsSettingsAndActive` (`killSwitchActive` true only after arming, false after `down`); `TestSetRefusedWhenAuthorizerDenies` (state and store unchanged); `TestSetPartialArgsKeepOtherField`; `TestSetBadArgsIsError`.
  Integration (`nsNet` gains `KillSwitch`, namespace test): `TestIntegrationKillSwitchUpDown` — `set killSwitch=true`, `up`, table exists, `down`, `nstest.Snapshot` equals the initial one; `TestIntegrationKillSwitchCrashRecovery` — arm, drop the daemon, `Recover` from the same store removes the table.
- [ ] **Step 2:** run → FAIL. **Step 3:** implement; `RealNet.KillSwitch` calls `netconf.AddKillSwitch` / `tx.Undo`; `DryNet` logs it. **Step 4:** `go test -race ./...` in `linux/` passes. **Commit** `feat(linux): kill switch setting and daemon integration`.

### Task 3: Command line

**Files:** Modify `linux/cli/main.go`, `linux/cli/cli_test.go`.

New commands: `yggtunnelctl killswitch on|off`, `yggtunnelctl lan on|off` (send `set`); `status` prints `Kill switch: off | armed (not connected) | active` and `Local network: allowed | blocked` (when the kill switch is on); `status --json` unchanged in shape plus the new fields.

- [ ] **Step 1: Tests** with the stub handler: `TestCLIKillSwitchOnSendsSet` (args `{"killSwitch":true}` only), `TestCLILanOffSendsSet` (`{"allowLan":false}`), `TestCLIKillSwitchBadArgument` (exit 2, usage), `TestCLIStatusShowsKillSwitchStates` (three cases from stub data).
- [ ] **Step 2:** FAIL; **Step 3:** implement and extend the usage text; **Step 4:** PASS. **Commit** `feat(linux): killswitch and lan commands`.

### Task 4: Window: the protection card

**Files:** Modify `linux/gui/src/controller.h/.cpp`; create `linux/gui/qml/ProtectionCard.qml`; modify `Main.qml`, `CMakeLists.txt` (resource list), `tests/tst_controller.cpp`, `tests/tst_window.cpp`, `translations/yggtunnel_ru.ts`.

**Interfaces — Produces** (on `ctl`): properties `killSwitch` (bool, setting), `allowLan` (bool), `killSwitchActive` (bool, from the daemon only); methods `setKillSwitch(bool)`, `setAllowLan(bool)` (send `set`; while the call is in flight the switches are disabled; the daemon's error text is shown in `lastError`).

Card behaviour: title «Protection»; switch «Kill switch» with the line «Traffic that is not going through the tunnel is dropped while you are connected.»; switch «Allow local network» (enabled only while the kill switch is on); a status line «Active» / «Armed: it starts with the next connection» / «Off». Shown whenever the daemon is reachable.

- [ ] **Step 1: Tests:** controller — `settingsAreReadFromStatus`, `setKillSwitchSendsSetAndDisablesWhileInFlight`, `killSwitchActiveComesOnlyFromTheDaemon` (set returns ok but status says inactive → property stays false), `setErrorIsShown`; window — `protectionCardShowsStates` (the three status lines), `lanSwitchDisabledWhileKillSwitchOff`, `togglingSendsSet` (click the switch → fake receives `set` with `killSwitch:true`), `protectionCardHiddenWhenUnreachable`, `screenshotProtection` (view the PNG, fix layout faults); the existing `translations` CTest must pass (Russian strings added).
- [ ] **Step 2:** FAIL; **Step 3:** implement; **Step 4:** `ctest --test-dir linux/gui/build` all pass, screenshots read. **Commit** `feat(gui): protection card with the kill switch`.

### Task 5: Docs and version

**Files:** Modify `linux/README.md`, `linux/README.ru.md`, `linux/CHANGELOG.md`, `linux/CHANGELOG.ru.md`, `linux/internal/version/VERSION` (`0.3.0`).

- [ ] **Step 1:** Document the feature: what it blocks and what it never blocks, the two settings and their defaults, the commands, the table `inet yggtunnelks` among «What it changes on the system», the limits (forwarded traffic; fail-open after a crash so the user is never locked out; `panic` removes it), how to check it by hand (`sudo nft list table inet yggtunnelks`). Changelog `0.3.0 — unreleased`. Remove «kill switch» from «Not yet».
- [ ] **Step 2:** Word and IP scan over the diff (as in plan 1, Task 12); `go vet ./... && go test ./...` in `linux/`; `ctest` in `linux/gui/build`.
- [ ] **Step 3: Commit** `docs(linux): kill switch, 0.3.0`.
