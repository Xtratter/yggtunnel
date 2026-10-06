# Status island (0.43) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans (native, no subagents).

**Goal:** an animated pill at the camera cutout on connection status changes, switchable in Settings.
**Spec:** docs/superpowers/specs/2026-10-06-status-island-design.md
**Deviation from the spec (simpler):** the switch is just the preference; the permission is requested when it is turned on, and without it the island works only inside the app (no switch bouncing back).

## Global Constraints
Kotlin only, no libraries, minSdk 26; neutral wording; Go core untouched; install on the phone only on request.

## Review Focus
- Same event twice in a row → one pill; reconnect (STARTING again) → one «Connecting».
- `addView` throwing (permission revoked, OEM limits) must not crash the VPN service.
- Main-thread only for view work; service threads post.
- Setting off → nothing shown and any shown pill removed.

### Task 1: model + tests — `StatusIslandModel.kt`, `StatusIslandModelTest.kt`
`enum Phase{OFF,STARTING,ON}`, `enum Kind{CONNECTING,CONNECTED,DISCONNECTED,FAILED}`, `data class Event(kind, detail)`,
`fun onPhase(prev: Phase?, now: Phase, error: String?): Event?`, `fun connected(ok, peers, viaServer): Event?`, `class Dedupe { fun accept(e: Event?): Event? }`.
Tests first (fail), then code. Commit.

### Task 2: view + window — `StatusIslandView.kt`, `StatusIsland.kt`
View (Canvas capsule, dot, text; progress 0..1; `ValueAnimator`s), window params, cutout geometry, in-app host, `show/preview/hide`. Compile. Commit.

### Task 3: wiring — `YggVpnService`, `MainActivity`, `SettingsCard`, `Prefs`, manifest, strings EN/RU
Service: `setState` → model → island; poll for `ok` after `ON` (≤30 s, cancelled by state change); `fail` → FAILED. Settings switch + permission screen + preview. `./gradlew testDebugUnitTest compileDebugKotlin -PskipGo` green. Commit.

### Task 4: 0.43 release prep
Bump 59/0.43, bilingual CHANGELOG + fastlane 59.txt, assembleRelease, signed test APK in builds dir. Stop before install/release.
