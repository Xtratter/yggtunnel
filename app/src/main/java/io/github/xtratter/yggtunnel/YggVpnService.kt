package io.github.xtratter.yggtunnel

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.Handler
import android.os.Looper
import android.system.OsConstants
import org.json.JSONObject

/**
 * The VPN: starts the Yggdrasil node and gives it a TUN — either only the Yggdrasil
 * network (200::/7), or, with a set-up server, all traffic through WireGuard to it. The system binds a running VpnService itself,
 * so it keeps living in the background without a notification of its own.
 */
class YggVpnService : VpnService() {
    enum class State { OFF, STARTING, ON }

    companion object {
        const val ACTION_START = "start"
        const val ACTION_STOP = "stop"
        const val ACTION_RESTART = "restart"
        const val ACTION_NOTIFICATION = "notification"
        const val ACTION_MONITOR = "monitor"

        /** The status-notification setting changed: show or remove it now. */
        fun notificationChanged(ctx: android.content.Context) {
            if (state != State.OFF) ctx.startService(Intent(ctx, YggVpnService::class.java).setAction(ACTION_NOTIFICATION))
        }

        /** The connection log was turned on or off: start or stop its checks now. */
        fun monitorChanged(ctx: android.content.Context) {
            if (state == State.ON) ctx.startService(Intent(ctx, YggVpnService::class.java).setAction(ACTION_MONITOR))
        }

        /** Reconnect with the new settings; several calls within a second make one reconnect. */
        fun restartSoon(ctx: android.content.Context) {
            if (state != State.OFF) ctx.startService(Intent(ctx, YggVpnService::class.java).setAction(ACTION_RESTART))
        }
        const val CHANNEL = "status"
        const val NOTIFICATION = 1
        @Volatile var state = State.OFF; private set
        @Volatile var error: String? = null; private set
    }

    private var netCallback: ConnectivityManager.NetworkCallback? = null
    private var monitor: ConnLog.Monitor? = null

    private fun startMonitor() {
        monitor?.stop()
        monitor = if (Prefs(this).connLog) ConnLog.Monitor(this).also { it.start() } else null
    }

    private fun stopMonitor() { monitor?.stop(); monitor = null }
    private val main = Handler(Looper.getMainLooper())

    /** Stop the node and start it again with the current settings; the service and its notification stay. */
    private val restart = Runnable {
        if (state == State.OFF) return@Runnable
        netCallback?.let { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) }
        netCallback = null
        stopMonitor()
        ConnLog.event(this, getString(R.string.log_reconnect))
        Prefs(this).sessionOpen = false // a reconnect on purpose, already logged
        Native.stop()
        startNode()
    }

    override fun onCreate() {
        super.onCreate()
        CrashLog.install(this) // the tile or always-on VPN can start the service without the app open
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stopNode()
            ACTION_MONITOR -> if (state == State.ON) startMonitor() else stopMonitor()
            ACTION_NOTIFICATION -> if (state != State.OFF) {
                if (Prefs(this).statusNotification) notifyStatus() else stopForeground(STOP_FOREGROUND_REMOVE)
            }
            ACTION_RESTART -> if (state != State.OFF) {
                main.removeCallbacks(restart)
                main.postDelayed(restart, 1200)
            }
            // ACTION_START from the app or the tile; the system's always-on VPN starts the service with
            // android.net.VpnService (or no action at all after a restart)
            else -> if (state == State.OFF) startNode()
        }
        return START_STICKY
    }

    private fun setState(s: State) {
        state = s
        StatusIsland.phase(this, StatusIslandModel.Phase.valueOf(s.name), error)
        YggTileService.refresh(this)
        // the status notification only when chosen in Settings: the system keeps a running VpnService
        // alive by itself and shows the key icon (0.6–0.10 always had it)
        if (s != State.OFF && Prefs(this).statusNotification) notifyStatus()
    }

    /** A quiet status notification with Disconnect; it also keeps the service in the foreground (MIUI kills less). */
    private fun notifyStatus() {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL) == null)
            nm.createNotificationChannel(NotificationChannel(CHANNEL, getString(R.string.notif_channel), NotificationManager.IMPORTANCE_LOW))
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val stop = PendingIntent.getService(this, 1, Intent(this, YggVpnService::class.java).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE)
        val n = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat)
            .setContentTitle(getString(when {
                state == State.STARTING -> R.string.state_connecting
                Prefs(this).tunnelServer != null -> R.string.state_on_server
                else -> R.string.state_on
            }))
            .setContentText(getString(R.string.app_name))
            .setContentIntent(open)
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, getString(R.string.disconnect), stop).build())
            .build()
        runCatching {
            if (Build.VERSION.SDK_INT >= 34) startForeground(NOTIFICATION, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
            else startForeground(NOTIFICATION, n)
        }.onFailure { runCatching { nm.notify(NOTIFICATION, n) } }
    }

    private fun startNode() {
        error = null
        setState(State.STARTING)
        Thread {
            val prefs = Prefs(this)
            val peers = prefs.effectivePeers
            val pinned = Prefs.serverPeers(prefs.server, prefs.serverLink).filter { it in peers }.joinToString(" ")
            val addr = Native.start(prefs.config, peers.joinToString("\n"), prefs.keepPeers, pinned, prefs.serverOnlyPeers)
            if (Native.isError(addr)) return@Thread fail(addr)
            val srv = prefs.tunnelServer
            val pfd = try {
                val b = Builder()
                    .setSession(getString(R.string.app_name))
                    .addAddress(addr, 7)
                    .setBlocking(false)
                // Per-app routing (apps uninstalled since are skipped). Android allows either an allow list or a
                // disallow list. The node's own peer connections must never go into the VPN: in the allow list
                // this app is simply not listed, in the disallow list it is added first.
                val only = prefs.includedApps.filter { it != packageName }
                if (prefs.onlyListedApps && only.isNotEmpty()) {
                    for (pkg in only) runCatching { b.addAllowedApplication(pkg) }
                } else {
                    b.addDisallowedApplication(packageName)
                    for (pkg in prefs.excludedApps) runCatching { b.addDisallowedApplication(pkg) }
                }
                if (srv == null) {
                    // Only Yggdrasil goes into the TUN: without allowFamily Android blocks all IPv4
                    // (no IPv4 address/route in the VPN means "block the family").
                    b.addRoute("200::", 7).setMtu(Native.mtu().coerceIn(1280, 65535)).allowFamily(OsConstants.AF_INET)
                } else {
                    // Everything through the server; Yggdrasil addresses still go straight into Yggdrasil (go/core/tunnel.go).
                    b.setMtu(1280)
                        .addAddress(srv.getString("clientIp4"), 32)
                        .addRoute("0.0.0.0", 0).addRoute("::", 0)
                        .addDnsServer("1.1.1.1").addDnsServer("8.8.8.8")
                    if (srv.optBoolean("ipv6")) b.addAddress(srv.getString("clientIp6"), 128)
                }
                b.establish()
            } catch (e: Exception) { return@Thread fail("error: ${e.message}") }
                ?: return@Thread fail("error: no VPN permission")
            val r = if (srv == null) Native.attachTun(pfd.detachFd()) else Native.attachTunnel(pfd.detachFd(), JSONObject()
                // an imported profile brings its own key; a set-up server uses this phone's
                .put("privateKey", prefs.server?.optString("clientKey")?.ifEmpty { null } ?: prefs.wgKeys.getString("private"))
                .put("serverKey", srv.getString("wgPublicKey"))
                .put("serverYgg", srv.getString("yggAddress"))
                .put("port", srv.getInt("wgPort"))
                .put("ipv6", srv.optBoolean("ipv6"))
                .put("clientIp4", srv.optString("clientIp4"))
                .put("lanes", prefs.lanes).put("laneUri", prefs.server?.let { Prefs.directPeerOf(it) } ?: "").toString())
            if (Native.isError(r)) return@Thread fail(r)
            watchNetwork()
            setState(State.ON)
            watchConnected(prefs.tunnelServer != null)
            if (prefs.sessionOpen) ConnLog.event(this, getString(R.string.log_unclosed))
            prefs.sessionOpen = true
            ConnLog.event(this, if (srv != null) getString(R.string.log_on_server2, prefs.serverLink.uppercase(), prefs.lanes) else getString(R.string.log_on))
            main.post { if (state == State.ON) startMonitor() }
            refreshPeersIfStale(prefs)
        }.start()
    }

    private var connectedWatch = 0

    /** The island's «connected»: up to 30 s after the node started, until a peer is up (and WireGuard has shaken hands). */
    private fun watchConnected(viaServer: Boolean) {
        if (!Prefs(this).statusIsland) return
        val id = ++connectedWatch
        Thread {
            repeat(30) {
                if (id != connectedWatch || state != State.ON) return@Thread
                val s = NodeStatus.read()
                if (s.ok(State.ON)) { StatusIsland.connected(this, true, s.up, viaServer); return@Thread }
                Thread.sleep(1000)
            }
        }.start()
    }

    private fun fail(msg: String) {
        ConnLog.event(this, getString(R.string.log_failed, msg.removePrefix("error: ")))
        main.post { stopMonitor() }
        Native.stop()
        error = msg.removePrefix("error: ")
        setState(State.OFF)
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    /** A new network (Wi-Fi ↔ mobile) → dial the peers at once instead of waiting for the retry timer. */
    private fun watchNetwork() {
        val cm = getSystemService(ConnectivityManager::class.java)
        val main = Handler(Looper.getMainLooper())
        netCallback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                val caps = cm.getNetworkCapabilities(network)
                ConnLog.event(this@YggVpnService, getString(R.string.log_network, getString(when {
                    caps?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_WIFI) == true -> R.string.log_wifi
                    caps?.hasTransport(android.net.NetworkCapabilities.TRANSPORT_CELLULAR) == true -> R.string.log_mobile
                    else -> R.string.log_other_net
                })))
                main.postDelayed({ Native.retryPeers() }, 1000)
            }
        }.also { cm.registerDefaultNetworkCallback(it) }
    }

    private fun stopNode() {
        main.removeCallbacks(restart)
        stopMonitor()
        ConnLog.event(this, getString(R.string.log_off))
        Prefs(this).sessionOpen = false
        netCallback?.let { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(it) }
        netCallback = null
        Native.stop()
        setState(State.OFF)
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    /** Once a week, while the peer list is the automatic one, a fresh list from the catalog — used from the next connect. */
    private fun refreshPeersIfStale(prefs: Prefs) {
        if (!prefs.peersAuto || System.currentTimeMillis() - prefs.peersUpdated < 7 * 24 * 3600_000L) return
        Thread {
            runCatching { PeerCatalog.fetch(prefs.server) }.getOrNull()?.takeIf { it.size >= 3 }?.let { prefs.setCatalogPeers(it) }
        }.start()
    }

    override fun onRevoke() = stopNode()

    override fun onDestroy() {
        if (state != State.OFF) stopNode()
        super.onDestroy()
    }
}
