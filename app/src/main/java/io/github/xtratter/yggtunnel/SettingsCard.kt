package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import android.widget.LinearLayout
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Dialog
import io.github.xtratter.uikit.M3Widgets

/** The Settings card and its dialogs: theme, apps, always-on, background work, switches (the peer ones live in PeersCard). */
class SettingsCard(private val a: MainActivity) {
    private companion object { @Volatile var islandPending = false }

    private val prefs = Prefs(a)
    private var appsText: TextView? = null
    private var batteryText: TextView? = null
    private var connLogText: TextView? = null

    private fun showConnLog() {
        connLogText?.text = if (prefs.connLog) a.getString(R.string.connlog_summary_on, prefs.connLogInterval) else a.getString(R.string.connlog_off)
    }

    fun addTo(col: LinearLayout) {
        val card = a.card()
        card.add(a.text(18f, face = M3.bold).apply { setText(R.string.settings) })
        fun row(title: Int, h: Int, ht: Int, onClick: () -> Unit, summary: (TextView) -> Unit) {
            val (r, s) = a.settingsRow(title, onClick)
            summary(s)
            card.add(r.help(ht, h), 4f)
        }
        row(R.string.theme, R.string.h_theme, R.string.h_theme_t, ::themeDialog) {
            it.text = a.getString(prefs.theme.title) + if (prefs.translucent) " · " + a.getString(R.string.translucency).lowercase() else ""
        }
        row(R.string.apps_title, R.string.h_apps, R.string.h_apps_t, { AppsUi.show(a) { showApps(); a.settingsChanged() } }) {
            appsText = it; showApps()
        }
        row(R.string.always_on, R.string.h_always, R.string.h_always_t, ::alwaysOnDialog) { it.setText(R.string.always_on_summary) }
        card.add(M3Widgets.switchRow(a, a.getString(R.string.auto_reconnect), prefs.autoReconnect) { prefs.autoReconnect = it }
            .help(R.string.h_reconnect_t, R.string.h_reconnect), 4f)
        card.add(M3Widgets.switchRow(a, a.getString(R.string.auto_connect), prefs.autoConnect) { prefs.autoConnect = it }
            .help(R.string.h_auto_connect_t, R.string.h_auto_connect), 4f)
        row(R.string.background_title, R.string.h_background, R.string.h_background_t, { batteryDialog(thenConnect = null) }) {
            batteryText = it; showBattery()
        }
        card.add(M3Widgets.switchRow(a, a.getString(R.string.status_notification), prefs.statusNotification) { on ->
            prefs.statusNotification = on
            if (on && Build.VERSION.SDK_INT >= 33 && a.checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
                a.requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 3)
            YggVpnService.notificationChanged(a)
        }.help(R.string.h_notification_t, R.string.h_notification), 4f)
        val islandRow = M3Widgets.switchRow(a, a.getString(R.string.status_island), prefs.statusIsland) { on ->
            when {
                !on -> { prefs.statusIsland = false; StatusIsland.disabled(); showIslandHeight() }
                StatusIsland.canOverlay(a) -> enableIsland()
                else -> {
                    // the switch turns on only once «display over other apps» is really granted (checkIsland, on return)
                    islandSwitch?.isChecked = false
                    islandPending = true
                    runCatching { a.startActivity(Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:" + a.packageName))) }
                }
            }
        }.help(R.string.h_island_t, R.string.h_island)
        islandSwitch = islandRow.getChildAt(1) as? android.widget.Switch
        card.add(islandRow, 4f)
        islandHeight = islandHeightRow().also { card.add(it, 4f) }
        showIslandHeight()
        card.add(M3Widgets.switchRow(a, a.getString(R.string.hide_addresses), Privacy.on) { on ->
            Privacy.set(a, on); if (on) Privacy.resume(); a.refreshAll()
        }.help(R.string.h_hide_t, R.string.h_hide), 4f)
        row(R.string.connlog_title, R.string.h_connlog, R.string.h_connlog_t, { ConnLogUi(a) { showConnLog() }.show() }) {
            connLogText = it; showConnLog()
        }
        row(R.string.backup_title, R.string.h_backup, R.string.h_backup_t, { BackupUi(a).show() }) { it.setText(R.string.backup_summary) }
        card.add(a.text(12.5f, M3.TEXT3).apply { setText(R.string.help_tip) }, 10f)
        col.add(card, 16f)
    }

    private fun showApps() {
        val only = prefs.onlyListedApps
        val n = (if (only) prefs.includedApps else prefs.excludedApps).size
        val count = a.resources.getQuantityString(R.plurals.apps_count, n, n)
        appsText?.text = when {
            n == 0 -> a.getString(R.string.apps_none)
            only -> a.getString(R.string.apps_only_summary, count)
            else -> a.getString(R.string.apps_bypass_summary, count)
        }
    }

    // ---- background work (battery optimisation) ----

    /** Not battery-optimised: Android leaves the VPN alone in the background. */
    fun backgroundAllowed() = a.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(a.packageName)

    private var islandSwitch: android.widget.Switch? = null
    private var islandHeight: LinearLayout? = null

    /** The height row is there while the island is on. */
    private fun showIslandHeight() { islandHeight?.visibility = if (prefs.statusIsland) android.view.View.VISIBLE else android.view.View.GONE }

    /** «Island height»: − and + (a step is 1 % of the screen height; every step shows the island at the new place). */
    private fun islandHeightRow(): LinearLayout {
        val value = a.text(16f, M3.TEXT).apply { gravity = android.view.Gravity.CENTER; minWidth = a.dp(56f) }
        fun showValue() { value.text = "${prefs.islandDrop} %" }
        fun step(delta: Int) {
            val v = (prefs.islandDrop + delta).coerceIn(0, 20)
            if (v == prefs.islandDrop) return
            prefs.islandDrop = v; showValue()
            io.github.xtratter.uikit.Haptics.play(io.github.xtratter.uikit.Haptics.Kind.TICK)
            val st = NodeStatus.read()
            StatusIsland.moved(a, YggVpnService.state, st.ok(YggVpnService.state), st.up, st.tunnel != null)
        }
        fun stepButton(label: String, delta: Int) = M3Widgets.button(a, label, M3Widgets.ButtonKind.TONAL) { step(delta) }
            .apply { setPadding(0, 0, 0, 0) }
        showValue()
        return LinearLayout(a).apply {
            gravity = android.view.Gravity.CENTER_VERTICAL
            minimumHeight = a.dp(56f)
            addView(a.text(16f).apply { setText(R.string.island_height) }, LinearLayout.LayoutParams(0, -2, 1f))
            addView(stepButton("−", -1), LinearLayout.LayoutParams(a.dp(48f), a.dp(40f)))
            addView(value)
            addView(stepButton("+", 1), LinearLayout.LayoutParams(a.dp(48f), a.dp(40f)))
        }.help(R.string.h_island_height_t, R.string.h_island_height)
    }

    private fun enableIsland() {
        prefs.statusIsland = true
        islandSwitch?.isChecked = true
        showIslandHeight()
        val st = NodeStatus.read()
        StatusIsland.preview(a, YggVpnService.state, st.ok(YggVpnService.state), st.up, st.tunnel != null)
    }

    /** Back from the permission screen (or any return): the island's switch follows what is really allowed. */
    fun checkIsland() {
        if (islandPending) {
            islandPending = false
            if (StatusIsland.canOverlay(a)) enableIsland()
        } else if (prefs.statusIsland && !StatusIsland.canOverlay(a)) {
            prefs.statusIsland = false // the permission was taken away
            islandSwitch?.isChecked = false
            showIslandHeight()
        }
    }

    fun showBattery() {
        batteryText?.setText(if (backgroundAllowed()) R.string.background_ok else R.string.background_limited)
        batteryText?.setTextColor(if (backgroundAllowed()) M3.TEXT2 else M3.WARN)
    }

    /** Explains and asks Android to let the app run in the background without limits; [thenConnect] — continue connecting on Later. */
    fun batteryDialog(thenConnect: (() -> Unit)?) {
        val ok = backgroundAllowed()
        val b = Theme.dialog(a).setTitle(R.string.background_title)
            .setMessage(if (ok) R.string.background_ok_text else R.string.background_text)
        if (!ok) b.setPositiveButton(R.string.background_allow) { _, _ ->
            runCatching { a.startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${a.packageName}"))) }
                .onFailure { appSettings() }
        }
        b.setNeutralButton(R.string.background_app_settings) { _, _ -> appSettings() }
        b.setNegativeButton(if (thenConnect != null) R.string.background_later else R.string.close) { _, _ -> thenConnect?.invoke() }
        b.create().apply { prestyle(); show() }
    }

    /** The app's page in Android settings — Battery → Unrestricted lives there (also on MIUI / HyperOS). */
    private fun appSettings() = a.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${a.packageName}")))

    // ---- dialogs ----

    /** Always-on VPN and "Block connections without VPN" live in the system settings; explain and open them. */
    private fun alwaysOnDialog() {
        Theme.dialog(a).setTitle(R.string.always_on).setMessage(R.string.always_on_text)
            .setPositiveButton(R.string.open_settings) { _, _ ->
                runCatching { a.startActivity(Intent(Settings.ACTION_VPN_SETTINGS)) }
                    .onFailure { a.startActivity(Intent(Settings.ACTION_WIRELESS_SETTINGS)) }
            }
            .setNegativeButton(R.string.close, null).create().apply { prestyle(); show() }
    }

    /** Theme and transparency, applied at once with the dialog staying open. */
    private fun themeDialog() {
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(8f), a.dp(20f), 0) }
        lateinit var d: AlertDialog
        fun fill() {
            box.removeAllViews()
            val group = RadioGroup(a)
            for (t in Theme.entries) group.addView(a.radio(a.getString(t.title), t == prefs.theme) {
                if (t != prefs.theme) { prefs.theme = t; a.rebuild(); M3Dialog.recolor(d); fill() }
            })
            box.addView(group)
            box.addView(a.divider(),
                LinearLayout.LayoutParams(-1, a.dp(1f)).apply { topMargin = a.dp(8f); bottomMargin = a.dp(4f) })
            box.addView(M3Widgets.switchRow(a, a.getString(R.string.translucency), prefs.translucent) { on ->
                prefs.translucent = on; a.rebuild(); M3Dialog.recolor(d); fill()
            }.help(R.string.h_translucency_t, R.string.h_translucency))
        }
        d = Theme.dialog(a).setTitle(R.string.theme).setView(ScrollView(a).apply { addView(box) })
            .setNegativeButton(R.string.close, null).create()
        fill()
        d.prestyle()
        d.show()
    }
}
