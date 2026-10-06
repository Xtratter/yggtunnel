package io.github.xtratter.yggtunnel

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.graphics.Typeface
import android.view.View

/**
 * The island itself: a black capsule that grows out of the camera cutout ([progress] 0 → 1, a little more while
 * it overshoots), then shows a coloured dot and the text. The window around it has the size of the full capsule.
 */
class StatusIslandView(ctx: Context, private val collapsedW: Int, private val collapsedH: Int) : View(ctx) {
    private val dp = ctx.resources.displayMetrics.density
    private val sp = ctx.resources.displayMetrics.scaledDensity
    private val fill = Paint(Paint.ANTI_ALIAS_FLAG).apply { color = 0xFF000000.toInt() }
    private val ring = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeWidth = 1.5f * dp }
    private val dotP = Paint(Paint.ANTI_ALIAS_FLAG)
    private val textP = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = 0xFFFFFFFF.toInt(); textSize = 14f * sp; typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
    }
    private val rect = RectF()
    private var text = ""
    private var accent = 0

    /** 0 — the capsule is the size of the cutout, 1 — full size. */
    var progress = 0f
        set(v) { field = v; invalidate() }

    /** The width the full capsule needs for the current text. */
    val fullWidth get() = (textP.measureText(text) + 44f * dp).toInt().coerceAtLeast(collapsedW)
    val fullHeight get() = (38f * dp).toInt().coerceAtLeast(collapsedH)

    fun set(text: String, accent: Int) { this.text = text; this.accent = accent; invalidate() }

    override fun onDraw(c: Canvas) {
        val p = progress.coerceAtLeast(0f)
        val cw = collapsedW + (width - collapsedW) * p
        val ch = collapsedH + (height - collapsedH) * p.coerceAtMost(1.15f)
        val l = (width - cw) / 2f
        val t = (height - ch) / 2f
        rect.set(l, t, l + cw, t + ch)
        c.drawRoundRect(rect, ch / 2f, ch / 2f, fill)
        val show = p.coerceAtMost(1f)
        ring.color = accent; ring.alpha = (150 * show).toInt()
        rect.inset(ring.strokeWidth / 2f, ring.strokeWidth / 2f)
        c.drawRoundRect(rect, ch / 2f, ch / 2f, ring)
        val a = ((show - 0.55f) / 0.45f).coerceIn(0f, 1f)
        if (a <= 0f) return
        val cy = height / 2f
        dotP.color = accent; dotP.alpha = (255 * a).toInt()
        c.drawCircle(l + 20f * dp, cy, 5f * dp * (0.6f + 0.4f * a), dotP)
        textP.alpha = (255 * a).toInt()
        c.drawText(text, l + 32f * dp, cy - (textP.ascent() + textP.descent()) / 2f, textP)
    }
}
