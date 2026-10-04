package io.github.xtratter.yggtunnel

import android.graphics.Typeface
import android.view.View
import android.widget.LinearLayout
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets
import io.github.xtratter.yggtunnel.YggVpnService.State

/** The status card (state, own Yggdrasil address, peers and tunnel numbers) and the Connect button with its animated edge. */
class StatusCard(private val a: MainActivity, onToggle: () -> Unit) {
    private val stateText = a.text(22f, face = M3.bold)
    private var address = ""
    private val addrText = a.text(14f, M3.primary, Typeface.MONOSPACE).apply {
        setOnClickListener { if (address.isNotEmpty()) a.copy(address) }
    }.help(R.string.h_address_t, R.string.h_address)
    private val infoText = a.text(13f, M3.TEXT2)
    private val toggle: TextView = M3Widgets.button(a, "") { onToggle() }.help(R.string.h_toggle_t, R.string.h_toggle)
    private var edge: EdgeGlow? = null
    private var shown: State? = null

    /** Adds the card and the button to [col]. */
    fun addTo(col: LinearLayout) {
        val card = a.card()
        card.add(stateText)
        card.add(addrText, 6f)
        card.add(infoText, 4f)
        card.help(R.string.h_state_t, R.string.h_state)
        col.add(card, 20f)
        col.add(toggle, 16f, a.dp(56f))
        edge = EdgeGlow(toggle)
    }

    fun start() = edge?.start()
    fun stop() = edge?.stop()

    fun update(s: NodeStatus, state: State) {
        val ok = s.ok(state)
        stateText.text = a.getString(when {
            state == State.OFF -> R.string.state_off
            !ok -> R.string.state_connecting
            s.tunnel != null -> R.string.state_on_server
            else -> R.string.state_on
        })
        stateText.setTextColor(if (ok) M3.OK else if (state == State.OFF) M3.TEXT else M3.WARN)
        address = s.address
        addrText.visibility = if (address.isEmpty()) View.GONE else View.VISIBLE
        addrText.text = Privacy.mask(a, address); Privacy.track(addrText)
        infoText.text = if (state == State.OFF) YggVpnService.error?.let { a.getString(R.string.error, it) } ?: a.getString(R.string.off_hint)
        else a.getString(R.string.info, s.up, s.peers.size, s.routing, Format.clock(s.uptime)) + (s.tunnel?.let {
            "\n" + a.getString(R.string.tunnel_info,
                if (s.shaken) a.getString(R.string.handshake_ago, it.optDouble("handshakeAgo").toLong()) else a.getString(R.string.handshake_none),
                Format.bytes(it.optLong("rx")), Format.bytes(it.optLong("tx")))
        } ?: "")
        // the edge: calm theme colours when off, amber and fast while connecting, green when connected
        val white = 0xFFFFFFFF.toInt()
        edge?.set(when {
            state == State.OFF -> intArrayOf(M3.tertiary, M3.mix(M3.primary, white, 0.55f), M3.secondary, M3.primary)
            !ok -> intArrayOf(M3.WARN, M3.tertiary, M3.WARN, M3.mix(M3.WARN, white, 0.5f))
            else -> intArrayOf(M3.OK, M3.primary, M3.mix(M3.OK, white, 0.5f), M3.tertiary)
        }, when { state == State.OFF -> 6000L; !ok -> 1600L; else -> 4000L })
        if (shown != state) {
            shown = state
            val on = state != State.OFF
            toggle.text = a.getString(if (on) R.string.disconnect else R.string.connect)
            toggle.setTextColor(if (on) M3.HOT else M3.ON_ACCENT)
            toggle.background = if (on) M3.pill(a, M3.withAlpha(M3.HOT, 0.12f), M3.withAlpha(M3.HOT, 0.35f)) else M3.pill(a, M3.primary)
        }
    }
}
