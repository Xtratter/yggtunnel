package io.github.xtratter.yggtunnel

import android.animation.ValueAnimator
import android.graphics.Canvas
import android.graphics.ColorFilter
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.PixelFormat
import android.graphics.RectF
import android.graphics.SweepGradient
import android.graphics.drawable.Drawable
import android.view.View
import android.view.ViewGroup
import android.view.animation.LinearInterpolator

/**
 * An animated edge for a pill button: a gradient running clockwise around it — a thin line right on the
 * edge, like the outline of the other buttons, only bolder (no glow: the user asked for a plain line).
 * Drawn in the parent's overlay. [set] changes colours and speed; [start] / [stop] follow the screen
 * being visible (no animation — no battery cost — in the background).
 */
class EdgeGlow(private val v: View) : Drawable() {
    private val dp = v.resources.displayMetrics.density
    private val line = 2.5f * dp
    private var colors = intArrayOf(0, 0)
    private var angle = 0f
    private val rect = RectF()
    private val m = Matrix()
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply { style = Paint.Style.STROKE; strokeCap = Paint.Cap.ROUND }
    private var shader: SweepGradient? = null
    private val anim = ValueAnimator.ofFloat(0f, 360f).apply {
        repeatCount = ValueAnimator.INFINITE
        interpolator = LinearInterpolator()
        addUpdateListener { angle = it.animatedValue as Float; invalidateSelf() }
    }

    init {
        (v.parent as ViewGroup).overlay.add(this)
        v.addOnLayoutChangeListener { _, l, t, r, b, _, _, _, _ ->
            setBounds(l, t, r, b); shader = null
        }
    }

    /** [c] — the colours running around the edge; [periodMs] — one turn. */
    fun set(c: IntArray, periodMs: Long) {
        colors = c + c[0] // closed loop: the last colour meets the first without a seam
        shader = null
        if (anim.duration != periodMs) {
            val f = anim.animatedFraction
            anim.duration = periodMs
            if (anim.isStarted) anim.currentPlayTime = (f * periodMs).toLong()
        }
        invalidateSelf()
    }

    fun start() { if (!anim.isStarted) anim.start() else anim.resume() }
    fun stop() = anim.pause()

    override fun draw(c: Canvas) {
        if (colors.size < 2 || bounds.isEmpty) return
        rect.set(bounds); rect.inset(line / 2, line / 2)
        val r = rect.height() / 2
        val sh = shader ?: SweepGradient(rect.centerX(), rect.centerY(), colors, null).also { shader = it }
        m.setRotate(angle, rect.centerX(), rect.centerY()) // grows clockwise on screen
        sh.setLocalMatrix(m)
        paint.shader = sh
        paint.strokeWidth = line
        c.drawRoundRect(rect, r, r, paint)
    }

    override fun setAlpha(alpha: Int) {}
    override fun setColorFilter(colorFilter: ColorFilter?) {}
    @Deprecated("Deprecated in Java")
    override fun getOpacity() = PixelFormat.TRANSLUCENT
}
