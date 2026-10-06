package io.github.xtratter.yggtunnel

import android.animation.Animator
import android.animation.AnimatorListenerAdapter
import android.animation.ValueAnimator
import android.content.Context
import android.graphics.PixelFormat
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.Gravity
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowManager
import android.view.animation.AccelerateInterpolator
import android.view.animation.OvershootInterpolator
import android.widget.FrameLayout
import io.github.xtratter.yggtunnel.StatusIslandModel.Event
import io.github.xtratter.yggtunnel.StatusIslandModel.Kind
import java.lang.ref.WeakReference

/**
 * The status island: a pill at the camera cutout when the connection changes. Over other apps when «display over
 * other apps» is allowed (a window of its own, not touchable), otherwise inside the open app. Off in Settings → nothing.
 * Can be called from any thread.
 */
object StatusIsland {
    private val main = Handler(Looper.getMainLooper())
    private val dedupe = StatusIslandModel.Dedupe()
    private var host: WeakReference<ViewGroup>? = null
    private var prev: StatusIslandModel.Phase? = null
    private var pill: Pill? = null


    /** The open main screen takes the island when there is no permission to draw over other apps. */
    fun registerHost(vg: ViewGroup?) { main.post { host = vg?.let { WeakReference(it) }; if (vg == null) pill?.takeIf { it.inApp }?.remove() } }

    fun canOverlay(ctx: Context) = Settings.canDrawOverlays(ctx)

    /** The service's state changed. */
    @Synchronized fun phase(ctx: Context, now: StatusIslandModel.Phase, error: String?) {
        val e = StatusIslandModel.onPhase(prev, now, error)
        prev = now
        show(ctx, e)
    }

    /** The node is really connected now (the service checks NodeStatus). */
    fun connected(ctx: Context, ok: Boolean, peers: Int, viaServer: Boolean) = show(ctx, StatusIslandModel.connected(ok, peers, viaServer))

    /** After the switch is turned on: shows how it looks, with the current state. */
    fun preview(ctx: Context, state: YggVpnService.State, ok: Boolean, peers: Int, viaServer: Boolean) {
        val e = when {
            state == YggVpnService.State.OFF -> Event(Kind.DISCONNECTED)
            ok -> StatusIslandModel.connected(true, peers, viaServer)
            else -> Event(Kind.CONNECTING)
        }
        if (e != null) present(ctx.applicationContext, e)
    }

    /** The height was changed: the pill on screen slides to the new place, or — when none is up — one is shown. */
    fun moved(ctx: Context, state: YggVpnService.State, ok: Boolean, peers: Int, viaServer: Boolean) {
        val app = ctx.applicationContext
        main.post {
            val p = pill
            if (p != null) p.nudge(topFor(app, p.view), 2200L) else preview(app, state, ok, peers, viaServer)
        }
    }

    /** The switch was turned off: take the pill away. */
    fun disabled() { main.post { pill?.remove() } }

    @Synchronized private fun show(ctx: Context, e: Event?) {
        if (!Prefs(ctx).statusIsland) return
        val ev = dedupe.accept(e) ?: return
        present(ctx.applicationContext, ev)
    }

    private fun present(app: Context, e: Event) {
        val text = when (e.kind) {
            Kind.CONNECTING -> app.getString(R.string.island_connecting)
            Kind.CONNECTED ->
                if (e.detail == "server") app.getString(R.string.island_connected_server)
                else app.getString(R.string.island_connected_peers, e.detail.toIntOrNull() ?: 0)
            Kind.DISCONNECTED -> app.getString(R.string.island_off)
            Kind.FAILED -> app.getString(R.string.island_failed) + e.detail.take(28).let { if (it.isEmpty()) "" else ": $it" }
        }
        // «Connecting» stays while the connection is being made (the next event turns it into «Connected» or «Failed»)
        val hold = when (e.kind) { Kind.CONNECTING -> 30_000L; Kind.CONNECTED -> 2600L; Kind.DISCONNECTED -> 2200L; Kind.FAILED -> 4000L }
        main.post { runCatching { (pill ?: makePill(app))?.present(text, e.kind, hold) } }
    }

    /** Where the capsule sits: the middle of the top cutout (or of the status bar) and the cutout's width. */
    private fun geometry(app: Context): Pair<Int, Int> {
        val dp = app.resources.displayMetrics.density
        if (Build.VERSION.SDK_INT >= 30) {
            val ins = app.getSystemService(WindowManager::class.java).currentWindowMetrics.windowInsets
            ins.displayCutout?.boundingRectTop?.takeIf { !it.isEmpty }?.let { return it.centerY() to it.width() }
            return ins.getInsets(WindowInsets.Type.statusBars()).top / 2 to (64 * dp).toInt()
        }
        return (12 * dp).toInt() to (64 * dp).toInt()
    }

    /** Where the window's top edge goes: the cutout's middle (or the status bar's) lowered by the set share of the screen height. */
    private fun topFor(app: Context, view: StatusIslandView): Int {
        val (cy, _) = geometry(app)
        return (cy - view.windowHeight / 2 + (app.resources.displayMetrics.heightPixels * Prefs(app).islandDrop / 100f).toInt()).coerceAtLeast(0)
    }

    private fun makePill(app: Context): Pill? {
        val dp = app.resources.displayMetrics.density
        // the service may run without the app open: the island still wears the app's theme
        Prefs(app).let { io.github.xtratter.uikit.M3.apply(app, it.theme.mode, it.translucent) }
        val view = StatusIslandView(app)
        // below the cutout by the set share of the screen height (Settings → Island height): the camera does not cover the text
        val top = topFor(app, view)
        var p: Pill
        if (canOverlay(app)) {
            val wm = app.getSystemService(WindowManager::class.java)
            val lp = WindowManager.LayoutParams(1, view.windowHeight, WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                    WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or WindowManager.LayoutParams.FLAG_LAYOUT_NO_LIMITS,
                PixelFormat.TRANSLUCENT).apply {
                gravity = Gravity.TOP or Gravity.CENTER_HORIZONTAL; y = top; title = "StatusIsland"
                if (Build.VERSION.SDK_INT >= 28) layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS
            }
            p = Pill(view, inApp = false, move = { y -> lp.y = y; wm.updateViewLayout(view, lp) }, top = top, resize = { w -> lp.width = w; wm.updateViewLayout(view, lp) },
                attach = { wm.addView(view, lp) }, detach = { wm.removeViewImmediate(view) })
            if (runCatching { p.attach() }.isSuccess) { pill = p; return p }
            // the system refused (an OEM restriction): inside the app, below
        }
        val vg = host?.get() ?: return null
        val lp = FrameLayout.LayoutParams(1, view.windowHeight, Gravity.TOP or Gravity.CENTER_HORIZONTAL).apply { topMargin = top }
        p = Pill(view, inApp = true, move = { y -> lp.topMargin = y; view.layoutParams = lp }, top = top, resize = { w -> lp.width = w; view.layoutParams = lp },
            attach = { vg.addView(view, lp) }, detach = { vg.removeView(view) })
        p.attach()
        pill = p
        return p
    }

    /** One pill on screen: a new event changes its text and bounces it, the hold timer starts again. */
    private class Pill(val view: StatusIslandView, val inApp: Boolean, val move: (Int) -> Unit, var top: Int, val resize: (Int) -> Unit, val attach: () -> Unit, val detach: () -> Unit) {
        private var anim: ValueAnimator? = null
        private val hold = Runnable { collapse() }

        private var maxW = 0
        private var moveAnim: ValueAnimator? = null

        /** The height setting changed while the pill is up: it slides to the new place and stays a bit longer. */
        fun nudge(newTop: Int, holdMs: Long) {
            moveAnim?.cancel()
            moveAnim = ValueAnimator.ofInt(top, newTop).apply {
                duration = 180; interpolator = OvershootInterpolator(1.0f)
                addUpdateListener { top = it.animatedValue as Int; move(top) }
                start()
            }
            if (view.progress >= 0.99f) { main.removeCallbacks(hold); main.postDelayed(hold, holdMs) }
        }

        fun present(text: String, kind: Kind, holdMs: Long) {
            main.removeCallbacks(hold); anim?.cancel()
            view.set(text, kind)
            maxW = maxOf(maxW, view.windowWidth()) // the window only grows while the pill is up: no cut edges
            resize(maxW)
            val from = if (view.progress >= 0.99f) 0.9f else view.progress
            run(from, 1f, 380, OvershootInterpolator(1.4f)) { main.postDelayed(hold, holdMs) }
        }

        private fun collapse() = run(view.progress, 0f, 260, AccelerateInterpolator()) { remove() }

        private fun run(from: Float, to: Float, ms: Long, ip: android.animation.TimeInterpolator, end: () -> Unit) {
            anim = ValueAnimator.ofFloat(from, to).apply {
                duration = ms; interpolator = ip
                addUpdateListener { view.progress = it.animatedValue as Float }
                addListener(object : AnimatorListenerAdapter() {
                    var cancelled = false
                    override fun onAnimationCancel(a: Animator) { cancelled = true }
                    override fun onAnimationEnd(a: Animator) { if (!cancelled) end() }
                })
                start()
            }
        }

        fun remove() {
            main.removeCallbacks(hold); anim?.cancel()
            runCatching { detach() }
            if (pill === this) pill = null
        }
    }
}
