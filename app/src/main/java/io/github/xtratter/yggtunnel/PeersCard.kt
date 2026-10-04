package io.github.xtratter.yggtunnel

import android.app.AlertDialog
import android.graphics.Typeface
import android.text.InputType
import android.text.TextUtils
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.RadioGroup
import android.widget.ScrollView
import android.widget.TextView
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Widgets

/** The peer list (state of every link, reserve, links not in the list), the editor with the public catalog and the peer settings. */
class PeersCard(private val a: MainActivity) {
    private val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL }

    private val prefs = Prefs(a)
    private var editText: TextView? = null
    private var linkText: TextView? = null
    private var lanesText: TextView? = null

    /** The list, then everything about peers as rows like in Settings: pick, edit, auto-pick, link, switches. */
    fun addTo(col: LinearLayout) {
        val card = a.card()
        card.add(a.text(18f, face = M3.bold).apply { setText(R.string.peers) }.help(R.string.h_peers_t, R.string.h_peers))
        card.add(box, 8f)
        card.help(R.string.h_peers_t, R.string.h_peers)
        card.add(a.divider(), 10f, a.dp(1f))
        fun row(title: Int, h: Int, ht: Int, onClick: () -> Unit, summary: (TextView) -> Unit) {
            val (r, s) = a.settingsRow(title, onClick)
            summary(s)
            card.add(r.help(ht, h), 4f)
        }
        row(R.string.pt_title, R.string.h_pt, R.string.pt_title, { PeerTestUi(a).show() }) { it.setText(R.string.pt_summary) }
        row(R.string.peers_edit_title, R.string.h_peers_edit, R.string.h_peers_edit_t, ::edit) { editText = it; showEdit() }
        row(R.string.auto_peers_title, R.string.h_auto, R.string.h_auto_t, ::keepDialog) { it.text = keepText(prefs.keepPeers) }
        row(R.string.server_link, R.string.h_server_link, R.string.h_server_link_t, ::linkDialog) { linkText = it; showLink() }
        row(R.string.lanes_title, R.string.h_lanes, R.string.lanes_title, ::lanesDialog) { lanesText = it; showLanes() }
        card.add(M3Widgets.switchRow(a, a.getString(R.string.server_only), prefs.serverOnlyPeers) { on ->
            prefs.serverOnlyPeers = on; a.settingsChanged()
        }.help(R.string.h_server_only_t, R.string.h_server_only), 4f)
        card.add(M3Widgets.switchRow(a, a.getString(R.string.own_peers), prefs.ownPeers) { on ->
            prefs.ownPeers = on
            // on: put them back into the stored list too, so turning it off later leaves them in place
            if (on) Prefs.serverPeers(prefs.server, prefs.serverLink).reversed().forEach { prefs.addPeerFirst(it) }
            a.refresh(); a.settingsChanged()
        }.help(R.string.h_own_peers_t, R.string.h_own_peers), 4f)
        col.add(card, 16f)
    }

    private fun showEdit() {
        editText?.text = a.getString(R.string.peers_edit_summary, prefs.effectivePeers.size,
            a.getString(if (prefs.peersAuto) R.string.peers_from_catalog else R.string.peers_by_hand))
    }

    private fun showLink() {
        linkText?.text = prefs.serverLink.uppercase() + if (prefs.serverLink == "tls") " · " + a.getString(R.string.recommended) else ""
    }

    private fun lanesLabel(n: Int) = if (n == 1) a.getString(R.string.lanes_one) else a.resources.getQuantityString(R.plurals.lanes_n, n, n)

    private fun showLanes() { lanesText?.text = lanesLabel(prefs.lanes) }

    /** How many links to the server carry the tunnel; more help the upload on a lossy mobile uplink. */
    private fun lanesDialog() {
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        box.addView(a.text(13f, M3.TEXT2).apply { setText(R.string.lanes_explain) })
        val group = RadioGroup(a)
        for (n in Prefs.LANES) group.addView(a.radio(lanesLabel(n), n == prefs.lanes) {
            if (n != prefs.lanes) { prefs.lanes = n; showLanes(); a.settingsChanged() }
            d.dismiss()
        })
        box.add(group, 6f)
        d = Theme.dialog(a).setTitle(R.string.lanes_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle(); d.show()
    }

    private fun keepText(n: Int) = if (n == 0) a.getString(R.string.keep_off)
        else a.resources.getQuantityString(R.plurals.keep_n, n, n) + if (n == Prefs.KEEP_PEERS) " · " + a.getString(R.string.recommended) else ""

    /** Auto-pick: how many peers to keep, with the reasoning behind the recommendation. */
    private fun keepDialog() {
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        lateinit var d: AlertDialog
        box.addView(a.text(13f, M3.TEXT2).apply { setText(R.string.keep_explain) })
        val group = RadioGroup(a)
        for (n in Prefs.KEEP_CHOICES) group.addView(a.radio(keepText(n), n == prefs.keepPeers) {
            if (n != prefs.keepPeers) { prefs.keepPeers = n; a.rebuild(); a.settingsChanged() }
            d.dismiss()
        })
        box.add(group, 6f)
        d = Theme.dialog(a).setTitle(R.string.auto_peers_title).setView(a.bounded(ScrollView(a).apply { addView(box) }))
            .setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle()
        d.show()
    }

    /** Which link to our server carries the traffic; the others stay up as fallbacks (Yggdrasil priorities). */
    private fun linkDialog() {
        lateinit var d: AlertDialog
        val box = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0) }
        box.addView(a.text(13f, M3.TEXT2).apply { setText(R.string.server_link_explain) })
        val group = RadioGroup(a)
        for (l in Prefs.LINKS) group.addView(a.radio(l.uppercase() + if (l == "tls") " · " + a.getString(R.string.recommended) else "", l == prefs.serverLink) {
            if (l != prefs.serverLink) { prefs.serverLink = l; showLink(); a.refresh(); a.settingsChanged() }
            d.dismiss()
        })
        box.add(group, 6f)
        d = Theme.dialog(a).setTitle(R.string.server_link).setView(box).setNegativeButton(android.R.string.cancel, null).create()
        d.prestyle(); d.show()
    }

    fun update(s: NodeStatus) {
        showEdit()
        box.removeAllViews()
        val configured = prefs.effectivePeers
        // Yggdrasil reports links without their options (?key=, ?priority=): matched by the base URI
        val byUri = s.peers.associateBy { Prefs.base(it.optString("uri")) }
        val reserve = s.reserve.map(Prefs::base).toSet()
        val bases = configured.map(Prefs::base).toSet()
        // also the links the node has that are not (or no longer) in the list — e.g. a removed peer until reconnect
        for (uri in configured + byUri.keys.filter { it !in bases }) {
            val p = byUri[Prefs.base(uri)]
            val up = p?.optBoolean("up") == true
            val row = LinearLayout(a).apply { orientation = LinearLayout.VERTICAL; setPadding(0, a.dp(6f), 0, a.dp(6f)) }
            val dot = when { Prefs.base(uri) in reserve -> "◇"; p == null -> "○"; up -> "●"; else -> "◌" }
            row.add(a.text(14f, if (up) M3.OK else M3.TEXT2, Typeface.MONOSPACE).apply {
                text = TextUtils.concat("$dot ", Privacy.mask(a, uri)); Privacy.track(this)
            })
            val detail = when {
                Prefs.base(uri) in reserve -> a.getString(R.string.peer_reserve)
                Prefs.base(uri) !in bases && p != null -> a.getString(R.string.peer_unlisted) +
                    (if (p.optBoolean("inbound")) " · " + a.getString(R.string.peer_inbound) else "") +
                    (if (up) " · " + Format.peerUp(a, p) else "")
                p == null -> null
                up -> Format.peerUp(a, p)
                else -> p.optString("error").ifEmpty { null }
            }
            if (detail != null) row.add(a.text(12f, M3.TEXT3).apply {
                text = Privacy.mask(a, detail); Privacy.track(this); setPadding(a.dp(18f), 0, 0, 0)
            })
            box.add(row.help(R.string.h_peers_t, R.string.h_peers))
        }
    }

    /** The peer list editor; Catalog fills it with fast reliable public peers timed from this phone. */
    private fun edit() {
        var catalog: String? = null
        val edit = EditText(a).apply {
            setText(prefs.effectivePeers.joinToString("\n"))
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            typeface = Typeface.MONOSPACE; textSize = 13f; setTextColor(M3.TEXT)
            setHorizontallyScrolling(true)
        }
        val note = a.text(12f, M3.TEXT3).apply {
            text = when {
                !prefs.peersAuto -> a.getString(R.string.peers_manual)
                prefs.peersUpdated > 0 -> a.getString(R.string.peers_auto_since, android.text.format.DateFormat.getDateFormat(a).format(prefs.peersUpdated))
                else -> a.getString(R.string.peers_auto_default)
            }
        }
        val body = LinearLayout(a).apply {
            orientation = LinearLayout.VERTICAL; setPadding(a.dp(20f), a.dp(4f), a.dp(20f), 0)
            add(a.text(13f, M3.TEXT2).apply {
                text = a.getString(R.string.peers_hint) + if (prefs.ownPeers && prefs.server != null) "\n" + a.getString(R.string.peers_own_note) else ""
            })
            add(edit, 8f)
            add(note, 8f)
        }
        val d = Theme.dialog(a).setTitle(R.string.peers).setView(body)
            .setPositiveButton(R.string.save, null)
            .setNeutralButton(R.string.peers_catalog, null)
            .setNegativeButton(android.R.string.cancel, null)
            .create()
        d.prestyle()
        d.setOnShowListener {
            d.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val text = edit.text.toString()
                val list = text.split(Regex("\\s+")).filter { it.isNotBlank() }
                if (catalog != null && text == catalog) prefs.setCatalogPeers(with(Prefs) { list.without(serverPeers(prefs.server, prefs.serverLink)) })
                else if (list != prefs.effectivePeers) { prefs.peers = text; prefs.peersAuto = false }
                a.settingsChanged()
                d.dismiss()
                a.refresh()
            }
            val cat = d.getButton(AlertDialog.BUTTON_NEUTRAL).help(R.string.h_catalog_t, R.string.h_catalog)
            cat.setOnClickListener {
                cat.isEnabled = false
                note.setText(R.string.peers_catalog_loading)
                Thread {
                    val r = runCatching { PeerCatalog.fetch(prefs.server) }
                    a.main.post {
                        cat.isEnabled = true
                        val list = r.getOrNull()
                        if (list == null || list.size < 3) { note.text = a.getString(R.string.peers_catalog_failed, r.exceptionOrNull()?.message ?: "—"); return@post }
                        val own = if (prefs.ownPeers) Prefs.serverPeers(prefs.server, prefs.serverLink) else emptyList()
                        catalog = (own + with(Prefs) { list.without(own) }).joinToString("\n")
                        edit.setText(catalog)
                        note.text = a.getString(R.string.peers_catalog_done, list.size)
                    }
                }.start()
            }
        }
        d.show()
    }
}
