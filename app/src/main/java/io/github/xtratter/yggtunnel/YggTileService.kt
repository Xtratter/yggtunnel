package io.github.xtratter.yggtunnel

import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/** Quick Settings tile: one tap connects or disconnects. */
class YggTileService : TileService() {
    override fun onStartListening() = update()

    override fun onClick() {
        if (YggVpnService.state != YggVpnService.State.OFF) {
            startService(Intent(this, YggVpnService::class.java).setAction(YggVpnService.ACTION_STOP))
        } else if (VpnService.prepare(this) != null) {
            // no VPN permission yet — the app asks for it
            val i = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK).setAction(MainActivity.ACTION_CONNECT)
            if (Build.VERSION.SDK_INT >= 34) startActivityAndCollapse(PendingIntent.getActivity(this, 0, i, PendingIntent.FLAG_IMMUTABLE))
            else @Suppress("DEPRECATION") startActivityAndCollapse(i)
        } else {
            startService(Intent(this, YggVpnService::class.java).setAction(YggVpnService.ACTION_START))
        }
        update()
    }

    private fun update() {
        val t = qsTile ?: return
        val s = YggVpnService.state
        val status = getString(when (s) {
            YggVpnService.State.OFF -> R.string.state_off
            YggVpnService.State.STARTING -> R.string.state_connecting
            YggVpnService.State.ON -> if (Prefs(this).tunnelServer != null) R.string.tile_server else R.string.state_on
        })
        t.state = if (s == YggVpnService.State.OFF) Tile.STATE_INACTIVE else Tile.STATE_ACTIVE
        // the status in the tile's title: some shades (MIUI / HyperOS) show only the title, not the subtitle
        t.label = status
        if (Build.VERSION.SDK_INT >= 29) t.subtitle = getString(R.string.app_name)
        if (Build.VERSION.SDK_INT >= 30) t.stateDescription = status
        t.updateTile()
    }

    companion object {
        /** Asks the system to redraw the tile after the VPN state changed. */
        fun refresh(ctx: Context) = runCatching { requestListeningState(ctx, ComponentName(ctx, YggTileService::class.java)) }
    }
}
