# Status island (0.43)

## Goal
A small animated pill at the top of the screen, around the camera cutout, that shows when the connection status
changes — on top of any app. It can be turned off in the menu.

## Events (one pill each, newest replaces the one on screen)
| Event | When | Look |
|---|---|---|
| Connecting | `State.STARTING` (also on a reconnect) | amber dot, «Подключение…» |
| Connected | node `ON` and `NodeStatus.ok` (a peer is up, and WireGuard has shaken hands when a server is set) | green dot, «Подключено» + «N пиров» / server |
| Disconnected | `ON/STARTING` → `OFF` by the user | grey dot, «VPN выключен» |
| Failed | `OFF` with `YggVpnService.error` | red dot, «Не удалось подключиться» (short reason) |
Not in scope: outage/restore from the connection-log checks (can be added later on the same pill).
The service has no «ok» event, so after `ON` it polls `NodeStatus.read()` once a second for up to 30 s;
if still not ok, no «Connected» pill (the main screen already says «connecting»). A reconnect within 1.2 s
(`restartSoon`) shows one «Connecting» pill, not several.

## Behaviour
- Setting «Status island» (switch in the Settings card, default OFF, help text). On → if «Display over other apps»
  is not granted, open that system screen for the app; the switch stays on only when the permission is there
  (checked on return, `onResume`).
- With the permission: the pill is a `TYPE_APPLICATION_OVERLAY` window (not touchable, not focusable, layout in
  screen, cutout mode ALWAYS), shown by `YggVpnService` (the process that knows the states).
- Without the permission but with the app open: the same view is added to the main screen's content root
  (in-app fallback). Without both: nothing.
- Position: horizontally centred; vertically centred on the top display cutout (`DisplayCutout.boundingRectTop`),
  on a phone without a cutout — in the middle of the status bar height.
- Animation: starts as a black capsule the size of the cutout, grows to the content width with an overshoot,
  the dot pulses once, text fades in; stays 2.5 s (4 s for Failed); shrinks back and disappears. Respects the
  system animation scale (0 → no animation, plain show/hide). Black fill with white text in every theme (it sits next to
  the camera); a thin accent-coloured ring for the state.
- No sound, no vibration (haptics setting unchanged), no focus stealing, never over the lock screen.

## Design
- `StatusIslandModel.kt` (no Android, tested): `Event`, mapping `(prev, state, ok, error) → Event?`, dedupe/debounce rules, texts keys.
- `StatusIslandView.kt`: the view and its animation (Canvas, `ValueAnimator`, no libraries).
- `StatusIsland.kt`: owns the window (`WindowManager`), permission check, in-app host registration, queue of one.
- `YggVpnService.setState` and `fail` call `StatusIsland.event`; `MainActivity.onResume/onPause` register the in-app host;
  `SettingsCard` gets the switch; `Prefs.statusIsland`; manifest `SYSTEM_ALERT_WINDOW`; strings EN/RU.

## Constraints
- Kotlin, no libraries; minSdk 26 (`TYPE_APPLICATION_OVERLAY`), cutout API 28+ (below: no cutout path).
- Neutral wording; no network code touched; Go core untouched.
- HyperOS may restrict overlays: if `canDrawOverlays` is true but adding the window throws, fall back to in-app and log it.

## Tests
- Unit: `StatusIslandModelTest` (every transition, dedupe of repeated states, reconnect → one Connecting, error text).
- On the phone (with the user's OK to install): each event, cutout alignment, dark/light, switch off, permission denied.

## Release
0.43, bilingual notes. Install on the phone only on request, with a warning.
