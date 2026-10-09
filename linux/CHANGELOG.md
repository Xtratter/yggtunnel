# Changelog (Linux)

[Русский](CHANGELOG.ru.md) · **English**

## 0.2.0 — unreleased

- The window `yggtunnel-gui` (Qt 6): state, server, peers, connect and disconnect, profile import by link or file,
  connection history and node log, «Disable everything»; an island with state changes inside the window;
  English and Russian; it talks only to the daemon
- The window tells in words when the daemon is not running or the socket is not accessible, and reconnects by itself

## 0.1.0 — unreleased

- First version: the daemon `yggtunneld` and the command `yggtunnelctl` — import a `yggtunnel://` profile,
  connect and disconnect a full tunnel, DNS through the tunnel, `panic` and automatic undo after a crash
- The platform-neutral Go core moved to `go/core`; the Android build is unchanged
