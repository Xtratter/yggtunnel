package io.github.xtratter.yggtunnel

import android.animation.ValueAnimator
import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.graphics.Typeface
import android.view.View
import android.view.animation.LinearInterpolator
import io.github.xtratter.uikit.M3
import io.github.xtratter.yggtunnel.StatusIslandModel.Kind

/**
 * The island itself, in the app's own Material 3 Expressive look: a tonal capsule (the theme's surface colour, a
 * hairline, a soft shadow) with a round state bubble — a spinner, a check, a power sign or an exclamation mark — and
 * the text. It comes out of blur ([progress] 0 → 1, a little more while it springs) and goes back into it. The view is
 * larger than the capsule by [pad] on every side, so the overshoot and the shadow are never cut by the window edge.
 */
class StatusIslandView(ctx: Context) : View(ctx) {
    private val dp = ctx.resources.displayMetrics.density
    private val sp = ctx.resources.displayMetrics.scaledDensity
    val pad = (22 * dp).toInt()
    private val capH = 40 * dp

    private val fill = Paint(Paint.ANTI_ALIAS_FLAG)
    private val line = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeWidth = 1f * dp }
    private val bubble = Paint(Paint.ANTI_ALIAS_FLAG)
    private val glyph = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeWidth = 2.2f * dp; strokeCap = Paint.Cap.ROUND; strokeJoin = Paint.Join.ROUND }
    private val textP = Paint(Paint.ANTI_ALIAS_FLAG).apply { textSize = 14f * sp; typeface = M3.bold }
    private val rect = RectF()
    private val arc = RectF()
    private val clip = Path()

    private var text = ""
    private var kind = Kind.CONNECTING
    private var capW = 0f
    private var widthAnim: ValueAnimator? = null
    private var spin = 0f
    private var spinner: ValueAnimator? = null

    /**
     * 0 — not there, 1 — shown (a little more while it springs). Showing and hiding are the same thing run
     * forwards and backwards: out of blur, fading in and growing from 80 %; and back into blur.
     */
    var progress = 0f
        set(v) {
            field = v
            val shown = v.coerceIn(0f, 1f)
            alpha = (shown / 0.6f).coerceAtMost(1f)
            if (android.os.Build.VERSION.SDK_INT >= 31) {
                val r = Math.round((1f - shown) * 18f * dp * 2f) / 2f // half-pixel steps: not a new effect on every frame
                if (r != blurR) {
                    blurR = r
                    setRenderEffect(if (r > 0.5f) android.graphics.RenderEffect.createBlurEffect(r, r, android.graphics.Shader.TileMode.CLAMP) else null)
                }
            }
            invalidate()
        }
    private var blurR = -1f

    init { alpha = 0f } // invisible until the first frame of the animation

    private fun capWidthFor(t: String) = textP.measureText(t) + 60 * dp

    /** The window size that holds the capsule for [t] with room around it. */
    fun windowWidth(t: String = text) = (capWidthFor(t) + 2 * pad).toInt()
    val windowHeight get() = (capH + 2 * pad).toInt()

    fun set(text: String, kind: Kind) {
        this.text = text; this.kind = kind
        fill.color = M3.surfaceContainerHigh
        line.color = M3.withAlpha(M3.TEXT, 0.12f)
        textP.color = M3.TEXT
        if (android.os.Build.VERSION.SDK_INT >= 28) fill.setShadowLayer(10 * dp, 0f, 3 * dp, 0x55000000)
        bubble.color = accent()
        glyph.color = if (Color.luminance(bubble.color) > 0.5f) 0xFF10131A.toInt() else 0xFFFFFFFF.toInt()
        val to = capWidthFor(text)
        widthAnim?.cancel()
        if (progress < 0.5f || capW == 0f) capW = to
        else widthAnim = ValueAnimator.ofFloat(capW, to).apply {
            duration = 240
            addUpdateListener { capW = it.animatedValue as Float; invalidate() }
            start()
        }
        spinning(kind == Kind.CONNECTING)
        invalidate()
    }

    private fun accent() = when (kind) {
        Kind.CONNECTING -> M3.WARN
        Kind.CONNECTED -> M3.OK
        Kind.FAILED -> M3.HOT
        Kind.DISCONNECTED -> M3.mix(M3.TEXT, M3.surfaceContainerHigh, 0.5f)
    }

    private fun spinning(on: Boolean) {
        if (!on) { spinner?.cancel(); spinner = null; return }
        if (spinner != null) return
        spinner = ValueAnimator.ofFloat(0f, 360f).apply {
            duration = 900; repeatCount = ValueAnimator.INFINITE; interpolator = LinearInterpolator()
            addUpdateListener { spin = it.animatedValue as Float; invalidate() }
            start()
        }
    }

    override fun onDetachedFromWindow() {
        super.onDetachedFromWindow()
        spinner?.cancel(); spinner = null; widthAnim?.cancel()
    }

    override fun onDraw(c: Canvas) {
        val p = progress
        val scale = 0.8f + 0.2f * p.coerceAtMost(1.35f)
        val cw = capW
        val l = (width - cw) / 2f
        val t = (height - capH) / 2f
        c.save()
        c.scale(scale, scale, width / 2f, height / 2f)
        rect.set(l, t, l + cw, t + capH)
        c.drawRoundRect(rect, capH / 2f, capH / 2f, fill)
        c.drawRoundRect(rect, capH / 2f, capH / 2f, line)
        // while the capsule changes its width for a new text, the content never goes beyond it
        clip.reset(); clip.addRoundRect(rect, capH / 2f, capH / 2f, Path.Direction.CW)
        c.clipPath(clip)
        val r = 14f * dp
        val cx = l + 6f * dp + r
        val cy = height / 2f
        c.drawCircle(cx, cy, r, bubble)
        drawGlyph(c, cx, cy)
        c.drawText(text, l + 6f * dp + 28f * dp + 10f * dp, cy - (textP.ascent() + textP.descent()) / 2f, textP)
        c.restore()
    }

    private fun drawGlyph(c: Canvas, cx: Float, cy: Float) {
        val u = dp
        when (kind) {
            Kind.CONNECTING -> {
                arc.set(cx - 6 * u, cy - 6 * u, cx + 6 * u, cy + 6 * u)
                c.drawArc(arc, spin, 250f, false, glyph)
            }
            Kind.CONNECTED -> {
                c.drawLine(cx - 5 * u, cy + 0.5f * u, cx - 1.5f * u, cy + 4 * u, glyph)
                c.drawLine(cx - 1.5f * u, cy + 4 * u, cx + 5 * u, cy - 3.5f * u, glyph)
            }
            Kind.DISCONNECTED -> {
                arc.set(cx - 5.5f * u, cy - 4.5f * u, cx + 5.5f * u, cy + 6.5f * u)
                c.drawArc(arc, -55f, 290f, false, glyph)
                c.drawLine(cx, cy - 6.5f * u, cx, cy - 0.5f * u, glyph)
            }
            Kind.FAILED -> {
                c.drawLine(cx, cy - 5.5f * u, cx, cy + 1.5f * u, glyph)
                c.drawPoint(cx, cy + 5f * u, glyph)
            }
        }
    }
}
