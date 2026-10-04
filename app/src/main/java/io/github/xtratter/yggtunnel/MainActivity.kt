package io.github.xtratter.yggtunnel

import android.app.Activity
import android.content.Intent
import android.graphics.Typeface
import android.net.VpnService
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Toast
import io.github.xtratter.uikit.Haptics
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import io.github.xtratter.yggtunnel.YggVpnService.State

/**
 * The only screen: lifecycle, the screen assembled from cards ([StatusCard], [ServerUi], [PeersCard],
 * [SettingsCard]), connecting, links and the file picker. Each card keeps its own views and dialogs.
 */
class MainActivity : Activity() {
    companion object {
        /** From the tile when the VPN permission is still missing. */
        const val ACTION_CONNECT = "io.github.xtratter.yggtunnel.CONNECT"
        private const val REQ_VPN = 1
        private const val REQ_FILE = 2
        private const val REQ_SAVE = 4
        private const val REQ_OPEN_BYTES = 5
    }

    val main = Handler(Looper.getMainLooper())
    private val serverUi by lazy { ServerUi(this) }
    private lateinit var status: StatusCard
    private lateinit var peers: PeersCard
    private lateinit var settings: SettingsCard
    private lateinit var serverBox: LinearLayout
    private var onFile: ((String) -> Unit)? = null
    private var onBytes: ((ByteArray) -> Unit)? = null
    private var toSave: ByteArray? = null

    private val tick = object : Runnable {
        override fun run() { refresh(); main.postDelayed(this, 1000) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CrashLog.install(this)
        Watchdog.start(this)
        Haptics.init(this, getSharedPreferences("prefs", MODE_PRIVATE))
        Privacy.load(this)
        Theme.apply(this)
        setContentView(build())
        handleLink(intent)
        val crash = CrashLog.take(this)
        crash?.let { main.post { showCrash(it) } }
        // auto-connect on a fresh start (not after rotation, not after a crash, not when opened by a profile link)
        if (savedInstanceState == null && crash == null && intent?.data == null && Prefs(this).autoConnect && YggVpnService.state == State.OFF)
            main.post { onToggle() }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleLink(intent)
    }

    override fun onResume() {
        super.onResume()
        settings.showBattery() // back from the battery settings
        // "as in the system" follows a dark-mode change made while the app was in the background
        if (!M3.isCurrent(this, Prefs(this).theme.mode, Prefs(this).translucent)) rebuild()
        main.post(tick)
        status.start()
        Privacy.resume()
    }

    override fun onPause() {
        super.onPause()
        main.removeCallbacks(tick)
        status.stop()
        Privacy.pause()
    }

    /** yggtunnel://import#… opened from a QR scanner, a messenger or the browser; or Connect from the tile. */
    private fun handleLink(intent: Intent?) {
        if (intent?.action == ACTION_CONNECT) {
            intent.action = null
            if (YggVpnService.state == State.OFF) main.post { onToggle() }
            return
        }
        val link = intent?.dataString ?: return
        if (link.startsWith("yggtunnel://")) main.post { ProfileUi.confirm(this, link) }
        intent.data = null
    }

    private fun build(): View {
        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16f), dp(16f), dp(16f), dp(24f))
        }
        col.add(text(30f, face = M3.heavy).apply { setText(R.string.app_name) }, 8f)
        col.add(text(14f, M3.TEXT2).apply { setText(R.string.subtitle) }, 2f)

        status = StatusCard(this, ::onToggle).also { it.addTo(col) }

        val server = card()
        server.add(text(18f, face = M3.bold).apply { setText(R.string.server) }.help(R.string.h_server_t, R.string.h_server))
        server.help(R.string.h_server_t, R.string.h_server)
        serverBox = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        server.add(serverBox, 8f)
        col.add(server, 16f)
        serverUi.render(serverBox)

        peers = PeersCard(this).also { it.addTo(col) }
        settings = SettingsCard(this).also { it.addTo(col) }

        col.add(M3Widgets.button(this, getString(R.string.log), M3Widgets.ButtonKind.OUTLINED) { showLog() }
            .help(R.string.h_log_t, R.string.h_log), 16f, dp(52f))

        return ScrollView(this).apply {
            isFillViewport = true
            addView(col)
            setOnApplyWindowInsetsListener { v, ins ->
                val b = ins.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout())
                v.setPadding(b.left, b.top, b.right, b.bottom); ins
            }
        }
    }

    private fun scrollView() = window.decorView.findViewById<ViewGroup>(android.R.id.content).getChildAt(0) as? ScrollView

    /** Applies the theme again and redraws the screen in place (no activity restart, the scroll position stays). */
    fun rebuild() {
        val y = scrollView()?.scrollY ?: 0
        Theme.apply(this)
        status.stop()
        setContentView(build())
        status.start()
        scrollView()?.let { sv -> sv.post { sv.scrollTo(0, y) } }
        refresh()
    }

    fun refresh() {
        val s = NodeStatus.read()
        status.update(s, YggVpnService.state)
        peers.update(s)
    }

    /** After the server or the peers changed. */
    fun refreshAll() {
        serverUi.render(serverBox)
        refresh()
    }

    /** After a change the running VPN only picks up on reconnect: reconnect by itself (default) or say so. */
    fun settingsChanged() {
        if (YggVpnService.state == State.OFF) return
        if (Prefs(this).autoReconnect) {
            YggVpnService.restartSoon(this)
            Toast.makeText(this, R.string.reconnecting, Toast.LENGTH_SHORT).show()
        } else Toast.makeText(this, R.string.reconnect_hint, Toast.LENGTH_LONG).show()
    }

    // ---- connecting ----

    private fun onToggle() {
        if (YggVpnService.state != State.OFF) {
            startService(Intent(this, YggVpnService::class.java).setAction(YggVpnService.ACTION_STOP))
            return
        }
        // once: Android may stop or slow a battery-optimised VPN in the background
        val prefs = Prefs(this)
        if (!settings.backgroundAllowed() && !prefs.batteryAsked) {
            prefs.batteryAsked = true
            settings.batteryDialog(thenConnect = ::onToggle)
            return
        }
        val ask = VpnService.prepare(this)
        if (ask != null) startActivityForResult(ask, REQ_VPN) else startVpn()
    }

    private fun startVpn() {
        startService(Intent(this, YggVpnService::class.java).setAction(YggVpnService.ACTION_START))
        refresh()
    }

    // ---- activity results: VPN permission, file picker ----

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQ_VPN && resultCode == RESULT_OK) startVpn()
        if (requestCode == REQ_FILE) {
            val text = data?.data?.takeIf { resultCode == RESULT_OK }?.let { uri ->
                runCatching { contentResolver.openInputStream(uri)?.use { it.readBytes().take(64 * 1024).toByteArray().decodeToString() } }.getOrNull()
            }
            if (text != null) onFile?.invoke(text)
            onFile = null
        }
        if (requestCode == REQ_SAVE) {
            val ok = data?.data?.takeIf { resultCode == RESULT_OK }?.let { uri ->
                runCatching { contentResolver.openOutputStream(uri)?.use { it.write(toSave) } != null }.getOrDefault(false)
            }
            toSave = null
            if (ok != null) Toast.makeText(this, if (ok) R.string.backup_saved else R.string.backup_save_failed, Toast.LENGTH_LONG).show()
        }
        if (requestCode == REQ_OPEN_BYTES) {
            val bytes = data?.data?.takeIf { resultCode == RESULT_OK }?.let { uri ->
                runCatching { contentResolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull()
            }
            if (bytes != null) onBytes?.invoke(bytes)
            onBytes = null
        }
    }

    /** Lets the user choose where to save [bytes] (Downloads, a cloud drive…) as [name]. */
    fun saveFile(name: String, bytes: ByteArray) {
        toSave = bytes
        startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
            .setType("application/octet-stream").putExtra(Intent.EXTRA_TITLE, name), REQ_SAVE)
    }

    /** Opens the file picker and passes the file's bytes to [cb]. */
    fun pickBytes(cb: (ByteArray) -> Unit) {
        onBytes = cb
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), REQ_OPEN_BYTES)
    }

    /** Opens the system file picker (Termux home is there too) and passes the file's text to [cb]. */
    fun pickFile(cb: (String) -> Unit) {
        onFile = cb
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), REQ_FILE)
    }

    // ---- log and crash report ----

    private fun showLog() {
        val log = Native.log().ifEmpty { getString(R.string.log_empty) }
        textDialog(R.string.log, Privacy.mask(this, log), log)
    }

    /** The previous run crashed: show what happened, with Copy — to send it to the developer. */
    private fun showCrash(crash: String) = textDialog(R.string.crash_title, crash, crash)

    /** A scrolling monospace text with Copy ([plain] is what gets copied — never the masked version). */
    private fun textDialog(title: Int, shown: CharSequence, plain: String) {
        val t = text(11f, M3.TEXT, Typeface.MONOSPACE).apply {
            text = shown; Privacy.track(this); setTextIsSelectable(true); setPadding(dp(20f), 0, dp(20f), 0)
        }
        Theme.dialog(this).setTitle(title).setView(bounded(ScrollView(this).apply { addView(t) }))
            .setPositiveButton(R.string.copy) { _, _ -> copy(plain) }
            .setNegativeButton(R.string.close, null)
            .create().apply { prestyle(); show() }
    }
}
