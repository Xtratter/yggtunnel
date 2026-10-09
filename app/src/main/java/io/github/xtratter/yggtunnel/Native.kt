package io.github.xtratter.yggtunnel

/** JNI bridge to libygg.so (go/jni.go). Errors come back as strings starting with "error: ". */
object Native {
    init { System.loadLibrary("ygg") }

    /** A fresh node config with a new key, JSON. */
    external fun generateConfig(): String
    /**
     * Starts the node; [peers] — URIs separated by whitespace. Auto-pick keeps the [keep] fastest
     * (0 — all) plus [pinned] (the server's own peers, "" — none); [serverOnly] — while a pinned peer is up,
     * all others are parked (go/core/peers.go). Returns the Yggdrasil address.
     */
    external fun start(config: String, peers: String, keep: Int, pinned: String, serverOnly: Boolean): String
    /** MTU of the Yggdrasil interface, after [start]. */
    external fun mtu(): Int
    /** Hands the TUN fd over to Go (Go closes it on [stop]). Returns "" or an error. */
    external fun attachTun(fd: Int): String
    /** Full tunnel: TUN → WireGuard inside Yggdrasil to the server (config JSON: go/core/tunnel.go TunnelConfig). */
    external fun attachTunnel(fd: Int, config: String): String
    external fun stop()
    external fun retryPeers()
    /** Node state JSON: running, address, publicKey, uptime, peers[uri, up, latencyMs, rx, tx, error]. */
    external fun status(): String
    /** The last lines of the node log. */
    external fun log(): String

    /** A new WireGuard key pair, JSON {private, public}. */
    external fun wgKeyPair(): String
    /** Starts the server setup over SSH in the background (params: see go/core/setup.go SetupParams). Returns "" or an error. */
    external fun setupStart(params: String): String
    /** Setup state JSON: running, log, hostKey, result, error. */
    external fun setupStatus(): String

    /** A QR code for [text]: JSON {size, rows: ["0101…"]}. */
    external fun qr(text: String): String

    /** Pings a Yggdrasil address from the running node: round trip in ms, or "error: …". */
    external fun pingYgg(dst: String, timeoutMs: Int): String
    /** Pings an IPv4 address through the WireGuard tunnel (full tunnel only): ms, or "error: …". */
    external fun pingInet(dst: String, timeoutMs: Int): String
    /** Starts the peer test in the background (params: go/peertest.go PeerTestParams). "" or an error. */
    external fun peerTestStart(params: String): String
    /** {running, total, results[uri, up, connectMs, latencyMs, rttMs, loss, kbps, error]} — finished ones, best first. */
    external fun peerTestStatus(): String
    external fun peerTestStop()
    /** Speed from the server's speed test (go/speed.go) to the running node: JSON {kbps, loss, bytes, ms} or "error: …". */
    external fun speedYgg(target: String, port: Int, token: String, size: Int): String

    /** Upload diagnostics (go/tcpdiag.go): TCP_INFO of the link sockets + WireGuard counters once a second. "ok" or "error: …". */
    external fun diagStart(seconds: Int): String
    external fun diagStop()
    /** {running, left, lines} */
    external fun diagStatus(): String
    external fun diagText(): String
    /** Congestion control of the live link sockets: how many took it, or "error: …". */
    external fun diagSetCC(name: String): String
    /** The unsent-bytes limit on the link sockets (TCP_NOTSENT_LOWAT), 0 — none; 128 KB by default. */
    external fun setLinkLowat(bytes: Int)
    /** Every goroutine's stack (go/hang.go), for a hang report. */
    external fun goStacks(): String
    /** Go's fd 2 (panic / fatal error traces) → [path]; "ok" or "error: …". */
    external fun redirectStderr(path: String): String
    external fun linkLowat(): Int

    fun isError(s: String) = s.startsWith("error: ")
}
