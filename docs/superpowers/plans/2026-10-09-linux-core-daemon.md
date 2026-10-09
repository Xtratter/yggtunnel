# Linux client, plan 1: core extraction, daemon, CLI, full tunnel

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `yggtunnelctl import <link>` then `yggtunnelctl up` brings up a full tunnel through the daemon, and `down` / `panic` / a daemon crash restore the previous network state.

**Architecture:** Platform-neutral Go core moves from `package main` into `go/core`; `go/jni.go` stays the only `main` file. A new Go module `linux/` holds the root daemon `yggtunneld` (core + netconf + nftables mark + resolved DNS + store + unix-socket JSON IPC) and the CLI `yggtunnelctl`. Every system change goes through a rollback stack.

**Tech Stack:** Go (module version from `go/go.mod`), `github.com/vishvananda/netlink`, `github.com/google/nftables`, `github.com/godbus/dbus/v5`, `golang.org/x/crypto`, systemd, polkit.

**Spec:** `docs/superpowers/specs/2026-10-09-linux-client-design.md`. This is plan 1 of 5: later plans are Qt window, kill switch, split routing, packaging/release.

## Global Constraints

- Neutral wording everywhere (code, comments, docs, commits): no mention of blocking, censorship, operators, throttling, «masking».
- No real IPs, ports, Ygg addresses, domains or keys in code or tests; examples only (`192.0.2.0/24`, `2001:db8::/32`, `200:db8::1`, test keys generated at runtime).
- Wire format frozen: `TestPacketEquivalence` and `packet_ref_test.go` move unchanged and stay green.
- `go/third_party/ironwood` and its patch files are not touched.
- Tunnel parameters copied from Android: MTU 1280, Yggdrasil address `/7`, `clientIp4/32`, `clientIp6/128` (only when `ipv6`), DNS `1.1.1.1` and `8.8.8.8`, interface `yggtun0`.
- Daemon socket `/run/yggtunnel.sock`, mode 0660, group `yggtunnel`; state in `/var/lib/yggtunnel/`, root-owned, files 0600.
- Docs bilingual (EN + RU), commits in English with author `Xtratter <1359019+Xtratter@users.noreply.github.com>`, trailer `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
- System settings of the laptop (routes, rules, nftables, resolved) are changed only in a network namespace by tests; real `up` on the host happens only in Task 11 after the user confirms.
- Nothing is pushed or released without the user's explicit word.

## Review Focus

- `up` while already `Starting`/`Connected` (two clients at once): second gets an error, state not corrupted. Test in Task 9.
- `down`/`panic` when `Off`: succeeds, changes nothing. Task 9.
- Daemon killed mid-`up`: next start rolls back from `prev.json`. Task 9.
- Leftover `yggtun0` from an earlier crash: removed before creating a new one. Task 7.
- Profile link with trailing text, bad base64, wrong `v`, missing keys, `ipv6` false with empty `clientIp6`: clear error, never a panic. Task 5.
- `systemd-resolved` not running: `up` fails with a message and rolls back; `/etc/resolv.conf` is never edited. Task 8.
- Peer without group `yggtunnel` / without polkit authorisation: command refused, nothing changed. Task 10.

---

### Task 1: Toolchain and baseline

**Files:** none changed.

- [ ] **Step 1:** Ask the user's permission, then `sudo pacman -S --needed go`. Run `go version` and compare with the `go` line in `go/go.mod`; if the installed Go is older, export `GOTOOLCHAIN=auto` (it downloads the required one) and note it in the plan log.
- [ ] **Step 2:** Run `cd go && go vet ./... && go test ./...` on the unmodified tree. Expected: PASS, except tests that need env vars or JDK headers; record any failure as baseline, do not fix it.
- [ ] **Step 3:** Record the baseline result in `docs/superpowers/plans/2026-10-09-linux-core-daemon.md` under a final «Baseline» line only if something fails; otherwise nothing to commit.

### Task 2: Move the core into `go/core`

**Files:**
- Move (`git mv`) from `go/` to `go/core/`: all `*.go` except `jni.go`, all `*.sh` (they are `go:embed`ded by `setup.go`, so they must sit next to it), all `*_test.go`.
- Modify: `go/jni.go`, `app/build.gradle.kts:44-45` (input file tree must now include `**/*.go`, `**/*.sh`).
- Keep in `go/`: `jni.go`, `build.sh`, `go.mod`, `go.sum`, `third_party/`.

**Interfaces:**
- Produces in package `core`: `func Default() *Node` (the process-wide node, replaces the `node` variable), `func LogString() string` (was `logSink.String()`), plus every identifier `jni.go` uses, exported with the same name capitalised. The compiler lists them.
- `jni.go` becomes `package main` importing `github.com/Xtratter/yggtunnel/go/core`; function bodies only change `node.X` → `core.Default().X` and similar.

- [ ] **Step 1: Write the guard test** `go/core/export_test.go`: `TestDefaultNodeIsSingleton` asserts `Default() == Default()` and `Default() != nil`. Run `cd go && go test ./core -run TestDefaultNodeIsSingleton`; expected FAIL (package does not exist yet).
- [ ] **Step 2:** `git mv` the files, change `package main` to `package core`, add `Default()`, export what `jni.go` needs. Do not change any logic.
- [ ] **Step 3:** Verify: `cd go && go vet ./... && go test ./...` → PASS, including `TestPacketEquivalence`. Then `GOOS=android GOARCH=arm64 go vet ./core/...` → no errors (pure Go, no NDK needed). `go vet` of the `main` package needs cgo and the NDK; if unavailable, state it in the commit message instead of claiming it was checked.
- [ ] **Step 4:** Update `app/build.gradle.kts` input tree; check with `git diff` that only that line changed.
- [ ] **Step 5:** Ask the user whether this counts as an app version (repository rule: every change is a version). If yes, bump `versionCode`/`versionName` and the changelogs (EN+RU, fastlane) in this same commit; otherwise note «no behaviour change» in the commit.
- [ ] **Step 6: Commit** `refactor: move platform-neutral core into go/core`.

### Task 3: `linux/` module scaffold

**Files:**
- Create: `linux/go.mod`, `linux/README.md`, `linux/README.ru.md`, `linux/internal/version/version.go`, `linux/internal/version/VERSION` (`0.1.0`).

**Interfaces:** Produces `version.String() string` returning the contents of `VERSION` (embedded).

`linux/go.mod`: module `github.com/Xtratter/yggtunnel/linux`, `require github.com/Xtratter/yggtunnel/go v0.0.0`, and two replaces: `github.com/Xtratter/yggtunnel/go => ../go` and `github.com/Arceliar/ironwood => ../go/third_party/ironwood` (replaces in a dependency's `go.mod` are ignored, so the patched copy must be repeated here).

- [ ] **Step 1: Failing test** `linux/internal/version/version_test.go`: `TestVersionNotEmpty` asserts `version.String()` matches `^\d+\.\d+\.\d+$`. Run `cd linux && go test ./internal/version` → FAIL.
- [ ] **Step 2:** Implement `version.String()` with `//go:embed VERSION` (the file sits next to `version.go`, since `go:embed` cannot reach a parent directory), trimmed of whitespace. Test passes.
- [ ] **Step 3:** Add a smoke test `linux/internal/smoke_test.go` importing `go/core` and calling `core.GenerateConfig()` (asserts non-empty JSON). Proves the replace chain, including the patched ironwood, resolves: `cd linux && go test ./...` → PASS.
- [ ] **Step 4: Commit** `feat(linux): module scaffold`.

### Task 4: IPC protocol and transport

**Files:**
- Create: `linux/internal/ipc/proto.go`, `linux/internal/ipc/server.go`, `linux/internal/ipc/client.go`, `linux/internal/ipc/ipc_test.go`.

**Interfaces — Produces:**
```go
type Request struct { ID int `json:"id"`; Cmd string `json:"cmd"`; Args json.RawMessage `json:"args,omitempty"` }
type Response struct { ID int `json:"id"`; OK bool `json:"ok"`; Error string `json:"error,omitempty"`; Data json.RawMessage `json:"data,omitempty"` }
type Event struct { Kind string `json:"event"`; Data json.RawMessage `json:"data,omitempty"` } // Kind: "state", "log", "peers"
type Handler interface { Handle(ctx context.Context, peer Peer, req Request) (any, error) }
type Peer struct { UID, GID, PID int }
func Listen(path string, group string, h Handler) (*Server, error) // creates the socket 0660 owned by group
func (s *Server) Broadcast(ev Event)
func (s *Server) Close() error
func Dial(path string) (*Client, error)
func (c *Client) Call(cmd string, args, out any) error
func (c *Client) Events() <-chan Event
```
Framing: one JSON object per line. `Peer` comes from `SO_PEERCRED`.

- [ ] **Step 1: Tests** in `ipc_test.go` (socket in `t.TempDir()`, group = current user's primary group): `TestCallRoundTrip` (echo handler returns `{"x":1}`), `TestHandlerErrorBecomesResponseError`, `TestBroadcastReachesAllClients` (two clients), `TestSocketMode0660`, `TestMalformedLineClosesOnlyThatClient`. Run → FAIL.
- [ ] **Step 2:** Implement. Unix stream socket; one goroutine per connection; broadcast through a per-client buffered channel, dropping a client whose buffer is full.
- [ ] **Step 3:** `cd linux && go test ./internal/ipc` → PASS.
- [ ] **Step 4: Commit** `feat(linux): JSON IPC over unix socket`.

### Task 5: Profile import

**Files:** Create `linux/internal/profile/profile.go`, `profile_test.go`.

**Interfaces — Produces:**
```go
type Profile struct {
    V int `json:"v"`; Name string `json:"name"`
    PrivateKey, ServerKey, ServerYgg string; Port int; IPv6 bool
    ClientIP4, ClientIP6 string; Peers []string; DirectPeer, WSSPeer string
}  // json tags: v,name,privateKey,serverKey,serverYgg,port,ipv6,clientIp4,clientIp6,peers,directPeer,wssPeer
func Parse(text string) (Profile, error)   // finds "yggtunnel://import#" in any text
func (p Profile) Link() string
func (p Profile) TunnelConfig() core.TunnelConfig
```
Rules copied from the Android `Profile.kt`: prefix `yggtunnel://import#`, base64url without padding, the data stops at the first character that is not a letter, digit, `-` or `_`; require `v == 1` and non-empty `privateKey`, `serverKey`, `serverYgg`.

- [ ] **Step 1: Tests:** `TestParseRoundTrip` (Link → Parse equal); `TestParseFindsLinkInsideText` (`"look: <link> thanks"`); table `TestParseRejects` with cases wrong `v`, missing `serverKey`, invalid base64, empty text, link without data, JSON that is an array. Each returns an error, none panics. `TestTunnelConfigMapsFields` (port, ipv6, clientIp4, serverYgg copied; `Lanes` 1). All values are examples (`200:db8::1`, `203.0.113.7`... use `192.0.2.x` for IPv4).
- [ ] **Step 2:** Run → FAIL. Implement. Run → PASS.
- [ ] **Step 3: Commit** `feat(linux): yggtunnel:// profile parsing`.

### Task 6: Store (state, profiles, secrets)

**Files:** Create `linux/internal/store/store.go`, `secrets.go`, `store_test.go`.

**Interfaces — Produces:**
```go
type Store struct{ Dir string }
func Open(dir string) (*Store, error)                       // creates dir 0700
func (s *Store) SaveProfile(p profile.Profile) error        // private key encrypted
func (s *Store) Profile() (profile.Profile, error)          // the active profile; error if none
func (s *Store) MaskedProfile() profile.Profile             // PrivateKey replaced by "…" + last 4 chars
func (s *Store) SavePrev(v PrevState) error
func (s *Store) Prev() (PrevState, bool, error)             // false when no file
func (s *Store) ClearPrev() error
type PrevState struct { Steps []Step `json:"steps"` }       // Step{Kind string; Args map[string]string}
```
Secrets: XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`), key generated once into `Dir/key` (0600). Files written atomically (temp + rename), mode 0600.

- [ ] **Step 1: Tests:** `TestProfileRoundTrip`; `TestProfileFileDoesNotContainPrivateKey` (reads the raw file, asserts the key string is absent); `TestFilesAre0600`; `TestMaskedProfileHidesKey`; `TestPrevRoundTripAndClear`; `TestPrevMissingIsNotError`; `TestCorruptProfileReturnsError`.
- [ ] **Step 2:** Run → FAIL; implement; run → PASS.
- [ ] **Step 3: Commit** `feat(linux): store with encrypted secrets and saved network state`.

### Task 7: netconf — transactional network changes

**Files:** Create `linux/internal/netconf/tx.go`, `link.go`, `route.go`, `mark.go`, `netconf_test.go`, `testns_test.go`.

**Interfaces — Produces:**
```go
type Tx struct{ /* undo stack */ }
func NewTx(rec func(store.PrevState) error) *Tx          // rec persists the steps after every Do
func (t *Tx) Do(step store.Step, apply, undo func() error) error // apply, push undo, persist; on apply error nothing is pushed
func (t *Tx) Rollback() error                            // undo in reverse order, continue past errors, return joined error
type Params struct { IfName string; YggAddr netip.Addr; ClientIP4 netip.Addr; ClientIP6 netip.Addr; MTU int; Table, Mark int; CgroupPath string }
func CreateTun(ifName string) (*os.File, error)          // /dev/net/tun, IFF_TUN|IFF_NO_PI, removes a leftover link with the same name first
func Configure(tx *Tx, p Params) error                   // addresses, MTU, link up, default routes in table p.Table, ip rules (not fwmark → table; main suppress_prefixlength 0), nft mark of the cgroup
func RecoverFrom(prev store.PrevState) error             // undoes steps recorded by a dead daemon, using Step.Kind/Args only
```
Step kinds: `link`, `addr`, `route`, `rule`, `nft`. Every undo must be reconstructible from `Step` alone (that is what `RecoverFrom` uses).

- [ ] **Step 1: Test helper** `testns_test.go`: `inNetns(t, fn)` re-executes the test binary under `unshare -rn` (unprivileged user + network namespace, gives `CAP_NET_ADMIN` inside) and skips with a clear message when `unshare` cannot create namespaces.
- [ ] **Step 2: Tests:** `TestTxRollbackOrder` (pure, three steps, undo order 3,2,1); `TestTxApplyErrorNotPushed`; `TestTxRollbackContinuesAfterUndoError`; `TestTxPersistsAfterEachStep` (recorder called once per Do). In netns: `TestCreateTunRemovesLeftover`, `TestConfigureThenRollbackRestoresRoutes` (snapshot of `ip -j route show table all` and `ip rule` before and after equal), `TestRecoverFromRestoresRoutes` (Configure, drop the Tx without rollback, call `RecoverFrom` with the persisted state, compare snapshots), `TestConfigureOmitsIPv6WhenClientIP6Invalid`.
- [ ] **Step 3:** Run → FAIL; implement with `vishvananda/netlink` and `google/nftables`; run → PASS (or skip with message if namespaces unavailable; a skip is reported, not hidden).
- [ ] **Step 4: Commit** `feat(linux): transactional netconf with rollback and recovery`.

### Task 8: DNS through systemd-resolved

**Files:** Create `linux/internal/dns/resolved.go`, `resolved_test.go`.

**Interfaces — Produces:**
```go
type Conn interface { Call(method string, args ...any) error }   // thin D-Bus seam for tests
func Set(c Conn, ifIndex int, servers []netip.Addr) error        // SetLinkDNS + SetLinkDomains(~.) + SetLinkDefaultRoute(true)
func Revert(c Conn, ifIndex int) error                           // RevertLink
func SystemConn() (Conn, error)                                  // system bus, org.freedesktop.resolve1; error text names «systemd-resolved is not running» when absent
```

- [ ] **Step 1: Tests with a fake `Conn`:** `TestSetCallsExpectedMethods` (order and arguments, servers `1.1.1.1`, `8.8.8.8` as address family + bytes), `TestSetStopsOnFirstError`, `TestRevertCallsRevertLink`, `TestSystemConnErrorMentionsResolved` (uses a bus address that does not exist).
- [ ] **Step 2:** Run → FAIL; implement with `godbus/dbus/v5`; run → PASS.
- [ ] **Step 3: Commit** `feat(linux): DNS through systemd-resolved`.

### Task 9: Daemon state machine

**Files:** Create `linux/internal/daemon/daemon.go`, `state.go`, `daemon_test.go`.

**Interfaces:**
- Consumes: Tasks 4–8 and `core`.
- Produces:
```go
type State string // "off","starting","connected","reconnecting","error"
type Core interface { // seam over *core.Node
    Start(configJSON string, peers []string) (yggAddr string, err error)
    AttachTunnel(fd int, cfg core.TunnelConfig) error
    Stop(); Status() string; MTU() int
}
type Net interface { // seam over netconf + dns
    Up(tx *netconf.Tx, p netconf.Params, dns []netip.Addr) (tun *os.File, err error)
}
type Daemon struct{ /* … */ }
func New(st *store.Store, c Core, n Net, emit func(ipc.Event)) *Daemon
func (d *Daemon) Handle(ctx context.Context, peer ipc.Peer, req ipc.Request) (any, error) // commands: "up","down","status","import","log","panic","version"
func (d *Daemon) Recover() error // call at start: roll back leftover Prev state
```
`up` order: save profile read → `Core.Start` → `Net.Up` (creates tun, addresses, routes, DNS) → `Core.AttachTunnel(fd, profile.TunnelConfig())` → state `connected`. Any error: `Rollback`, `Core.Stop`, state `error` then `off`. `status` returns state, error text, `Core.Status()` JSON and the masked profile.

- [ ] **Step 1: Tests with fakes:** `TestUpHappyPathOrder` (calls recorded in the order above, state `connected`, events `starting`→`connected`); `TestUpFailureRollsBack` (fake `Net.Up` fails after two recorded steps → rollback ran, `Core.Stop` called, state `off`, `ClearPrev` done); `TestSecondUpWhileStartingRefused` (blocks the first with a channel); `TestDownWhenOffIsNoop`; `TestPanicWhenOffIsNoop`; `TestPanicRollsBackFromPrevFile` (state files present, no running tunnel); `TestRecoverRollsBackLeftoverPrev`; `TestUpWithoutProfileErrors`; `TestImportStoresProfileAndStatusIsMasked`.
- [ ] **Step 2:** Run → FAIL; implement (one mutex, no goroutine mutates state outside it); run → PASS.
- [ ] **Step 3: Commit** `feat(linux): daemon state machine with rollback`.

### Task 10: Real wiring, polkit and the daemon binary

**Files:** Create `linux/daemon/main.go`, `linux/internal/daemon/real.go` (adapters `*core.Node` → `Core`, netconf+dns → `Net`), `linux/internal/auth/polkit.go`, `polkit_test.go`, `linux/packaging/yggtunneld.service`, `linux/packaging/io.github.xtratter.yggtunnel.policy`.

**Interfaces — Produces:**
```go
type Authorizer interface { Check(peer ipc.Peer, action string) error }
func NewPolkit() (Authorizer, error)   // org.freedesktop.PolicyKit1.Authority.CheckAuthorization, subject unix-process{pid, start-time}
```
Actions: `io.github.xtratter.yggtunnel.connect` (up, down, panic, import). `status`, `log`, `version` need only socket access. `Handle` calls the authoriser before state-changing commands; tests use a fake authoriser.

- [ ] **Step 1: Tests:** `TestHandleRefusesWhenAuthorizerDenies` (no state change, error text mentions authorisation), `TestReadOnlyCommandsSkipAuthorizer`, `TestPolkitSubjectUsesPID` (fake bus connection records the subject).
- [ ] **Step 2:** Implement; `main.go` flags: `--state-dir`, `--socket`, `--dry-run` (netconf and nft steps are only logged, tunnel not created), `--no-polkit` (development only, refuses to start unless `--dry-run` or an environment variable named `YGGTUNNEL_DEV` is set). Unit: `User=root`, `Delegate=yes` is not needed; `CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/yggtunnel`, `Restart=on-failure`; `ExecStopPost` runs `yggtunnelctl panic` so a killed daemon cannot leave rules behind.
- [ ] **Step 3:** `cd linux && go vet ./... && go test ./...` → PASS; `go build ./daemon` produces a binary.
- [ ] **Step 4: Commit** `feat(linux): yggtunneld daemon, polkit authorisation, systemd unit`.

### Task 11: CLI, namespace integration test, host verification

**Files:** Create `linux/cli/main.go`, `linux/cli/cli_test.go`, `linux/internal/daemon/integration_test.go`.

CLI: `yggtunnelctl [--socket PATH] import <link-or-file|-> | up | down | status [--json] | log | panic | version`. Output is short, errors on stderr, exit code 1 on failure.

- [ ] **Step 1: Tests:** `TestCLIStatusPrintsState` and `TestCLIImportReadsStdin` against an in-process daemon with fakes; `TestCLIExitCodeOnError`.
- [ ] **Step 2: Integration test** (netns, skipped with a message when unavailable): daemon with the real `Net` and a fake `Core`, `up` → asserts `yggtun0` exists with `clientIp4/32`, MTU 1280, default route in table, rule present; `down` → route and rule snapshots equal the initial ones; second run after a simulated crash → `Recover` restores them.
- [ ] **Step 3:** Run → PASS. **Commit** `feat(linux): yggtunnelctl and namespace integration test`.
- [ ] **Step 4 (host verification, only with the user's confirmation after a warning that the network can drop):** install the built binaries to `/usr/local/bin` and the unit to `/etc/systemd/system`, `systemctl start yggtunneld`, `yggtunnelctl import` with the user's own profile (the user pastes it; never written into the repo or logs), `yggtunnelctl up`, compare the egress IP with the server's, `yggtunnelctl down`, compare routes before and after. Report the real outcome, including failures. Keep `yggtunnelctl panic` ready.

### Task 12: Docs and version

**Files:** Create/modify `linux/README.md`, `linux/README.ru.md`, `linux/CHANGELOG.md`, `linux/CHANGELOG.ru.md`, root `README.md` / `README.ru.md` (one link line each).

- [ ] **Step 1:** Write install-from-source, the `yggtunnelctl` commands, the group `yggtunnel` setup, the rollback/panic behaviour, and the known limits (no window yet, no kill switch, no split routing). Neutral wording, examples only.
- [ ] **Step 2:** Grep the repo diff for forbidden words and for IP-like strings outside documentation prefixes: `git diff main --name-only | xargs grep -inE 'block|censor|throttl|mask' ` (review hits manually; «masked profile» in code is about secrets and is fine) and `grep -rnE '\b([0-9]{1,3}\.){3}[0-9]{1,3}\b'` limited to the new files, allowing only `192.0.2.`, `198.51.100.`, `203.0.113.`, `1.1.1.1`, `8.8.8.8`, `0.0.0.0`.
- [ ] **Step 3: Commit** `docs(linux): readme and changelog for 0.1.0`. Tagging and release only on the user's word.
