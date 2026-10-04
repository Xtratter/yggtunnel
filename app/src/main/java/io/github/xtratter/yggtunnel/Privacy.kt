package io.github.xtratter.yggtunnel

import android.content.Context
import android.os.SystemClock
import android.view.Choreographer
import android.widget.TextView
import java.util.WeakHashMap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.text.SpannableString
import android.text.Spanned
import android.text.style.ReplacementSpan
import io.github.xtratter.uikit.M3
import kotlin.random.Random

/**
 * "Hide addresses" (for screenshots): every address on screen — ours and public ones, Yggdrasil
 * addresses, IPs, domains, peer URIs, wss paths, device names — is drawn as a grainy smear instead
 * of text, like a spoiler. Under the grain lies a random string of the same length, not the real
 * one, so nothing can be recovered from a screenshot. Display only — copying still copies the
 * real value, and everything keeps working. The grain is alive, like a spoiler in Telegram: specks
 * twinkle and drift while the screen is visible (about 30 frames a second, stopped in the background).
 */
object Privacy {
    @Volatile var on = false

    // ---- the grain animation: masked TextViews are redrawn while the app is in front ----
    private val views = WeakHashMap<TextView, Boolean>()
    private var running = false
    private var odd = false
    private val frame = object : Choreographer.FrameCallback {
        override fun doFrame(t: Long) {
            if (!running) return
            odd = !odd
            if (odd) views.keys.toList().forEach { v -> if (v.isAttachedToWindow && v.isShown) v.invalidate() }
            if (views.isNotEmpty() && on) Choreographer.getInstance().postFrameCallback(this)
        }
    }

    /** [tv] shows masked text: keep its grain moving. */
    fun track(tv: TextView) {
        if (!on) return
        views[tv] = true
        if (running) { Choreographer.getInstance().removeFrameCallback(frame); Choreographer.getInstance().postFrameCallback(frame) }
    }

    fun resume() { running = true; Choreographer.getInstance().removeFrameCallback(frame); Choreographer.getInstance().postFrameCallback(frame) }
    fun pause() { running = false; Choreographer.getInstance().removeFrameCallback(frame) }

    fun load(ctx: Context) { on = ctx.getSharedPreferences("prefs", Context.MODE_PRIVATE).getBoolean("hide_addresses", false) }

    fun set(ctx: Context, v: Boolean) {
        on = v
        ctx.getSharedPreferences("prefs", Context.MODE_PRIVATE).edit().putBoolean("hide_addresses", v).apply()
    }

    // order matters: the earlier patterns win where matches overlap
    private val patterns = listOf(
        Regex("(?<=://)[^\\s,·]+"),                                            // everything after scheme:// in peer URIs
        Regex("(?<=@)[^\\s:]+(:\\d+)?"),                                       // user@HOST:PORT
        Regex("\\b[23][0-9a-f]{2}:[0-9a-f:]{3,}[0-9a-f]\\b"),                  // Yggdrasil addresses 200::/7
        Regex("\\b(?:\\d{1,3}\\.){3}\\d{1,3}(:\\d+)?\\b"),                    // IPv4 (and :port)
        Regex("\\b(?:[a-z0-9-]+\\.)+[a-z]{2,}(:\\d+)?(/\\S*)?", RegexOption.IGNORE_CASE), // domains
    )

    /** The parts of [text] to cover: addresses by [patterns] and the literal [extra] values; no overlaps. Pure — unit-tested. */
    fun ranges(text: String, vararg extra: String): List<IntRange> {
        val taken = BooleanArray(text.length)
        val out = mutableListOf<IntRange>()
        fun add(r: IntRange) {
            if (r.isEmpty() || r.any { taken[it] }) return
            r.forEach { taken[it] = true }; out += r
        }
        for (p in patterns) p.findAll(text).forEach { add(it.range) }
        for (e in extra) if (e.isNotEmpty()) Regex(Regex.escape(e)).findAll(text).forEach { add(it.range) }
        return out.sortedBy { it.first }
    }

    /** [s] with addresses covered when the mode is on; [extra] — more literal values to cover (device names). */
    fun mask(ctx: Context, s: CharSequence, vararg extra: String): CharSequence {
        if (!on || s.isEmpty()) return s
        val text = s.toString()
        val ranges = ranges(text, *extra)
        if (ranges.isEmpty()) return s
        val out = SpannableString(s)
        val dp = ctx.resources.displayMetrics.density
        for (r in ranges) out.setSpan(GrainSpan(r.last - r.first + 1, text.substring(r).hashCode(), dp), r.first, r.last + 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
        return out
    }

    /** Draws [len] characters' worth of Gaussian-blurred random text with living grain on a soft pill. */
    private class GrainSpan(private val len: Int, private val seed: Int, private val dp: Float) : ReplacementSpan() {
        private val fake: String = Random(seed).let { r -> String(CharArray(len) { "0123456789abcdef:."[r.nextInt(18)] }) }

        override fun getSize(paint: Paint, text: CharSequence?, start: Int, end: Int, fm: Paint.FontMetricsInt?): Int {
            fm?.let { paint.getFontMetricsInt(it) }
            return paint.measureText(fake).toInt()
        }

        override fun draw(c: Canvas, text: CharSequence?, start: Int, end: Int, x: Float, top: Int, y: Int, bottom: Int, paint: Paint) {
            val w = paint.measureText(fake)
            val fm = paint.fontMetrics
            val rect = RectF(x, y + fm.ascent, x + w, y + fm.descent)
            val color = paint.color
            val bg = Paint(Paint.ANTI_ALIAS_FLAG).apply { this.color = M3.withAlpha(color, 0.10f) }
            c.drawRoundRect(rect, 4 * dp, 4 * dp, bg)
            // the random string, Gaussian-blurred until no glyph shape is left
            val b = Blur.text(fake, paint)
            val bp = Paint(Paint.FILTER_BITMAP_FLAG).apply { this.color = M3.withAlpha(color, 0.55f) }
            c.save()
            c.clipRect(rect.left - 2 * dp, rect.top - 2 * dp, rect.right + 2 * dp, rect.bottom + 2 * dp)
            c.drawBitmap(b.bitmap, x - b.pad, y + fm.ascent - b.pad, bp)
            c.restore()
            // grain: specks drift across the pill (wrapping around) and twinkle — like Telegram's spoiler
            val g = Paint(Paint.ANTI_ALIAS_FLAG)
            val rnd = Random(seed xor 0x5f3759df)
            val t = SystemClock.uptimeMillis() / 1000f
            val count = (rect.width() * rect.height() / (dp * dp * 3.5f)).toInt().coerceIn(12, 900)
            repeat(count) {
                val bx = rnd.nextFloat(); val by = rnd.nextFloat()
                val ph = rnd.nextFloat() * 6.28f; val sp = 3f + rnd.nextFloat() * 5f
                // speed in dp per second, the same for every pill: as a share of the pill's width (0.10.2–0.10.3)
                // long addresses had much faster grain than short ones. ±10 / ±3.5 dp/s — the short "your IP" pill
                // the user liked
                val vx = (rnd.nextFloat() - 0.5f) * 20f * dp; val vy = (rnd.nextFloat() - 0.5f) * 7f * dp
                val size = (0.35f + rnd.nextFloat() * 0.55f) * dp
                val tw = 0.5f + 0.5f * kotlin.math.sin(t * sp + ph)
                g.color = M3.withAlpha(color, 0.12f + 0.62f * tw)
                val px = ((bx * rect.width() + vx * t) % rect.width() + rect.width()) % rect.width()
                val py = ((by * rect.height() + vy * t) % rect.height() + rect.height()) % rect.height()
                c.drawCircle(rect.left + px, rect.top + py, size, g)
            }
        }
    }

    /** Gaussian blur of a text line (3 box-blur passes each way ≈ Gaussian), cached: rows are redrawn every frame. */
    private object Blur {
        class Img(val bitmap: android.graphics.Bitmap, val pad: Float)
        private val cache = object : android.util.LruCache<String, Img>(96) {}

        fun text(s: String, paint: Paint): Img {
            val key = "$s|${paint.textSize}|${paint.typeface?.hashCode()}"
            cache.get(key)?.let { return it }
            val r = (paint.textSize * 0.32f).toInt().coerceAtLeast(3)   // blur radius ≈ a third of the glyph height
            val pad = r * 3f
            val fm = paint.fontMetrics
            val w = (paint.measureText(s) + pad * 2).toInt().coerceAtLeast(1)
            val h = (fm.descent - fm.ascent + pad * 2).toInt().coerceAtLeast(1)
            val bmp = android.graphics.Bitmap.createBitmap(w, h, android.graphics.Bitmap.Config.ALPHA_8)
            val tp = Paint(paint).apply { color = 0xFF000000.toInt(); alpha = 255 }
            Canvas(bmp).drawText(s, pad, pad - fm.ascent, tp)
            // rows of an ALPHA_8 bitmap may be padded: copy with its real stride
            val stride = bmp.rowBytes
            val a = ByteArray(stride * h).also { bmp.copyPixelsToBuffer(java.nio.ByteBuffer.wrap(it)) }
            val px = IntArray(w * h) { i -> a[(i / w) * stride + i % w].toInt() and 0xFF }
            repeat(3) { boxH(px, w, h, r); boxV(px, w, h, r) }
            for (i in px.indices) a[(i / w) * stride + i % w] = px[i].toByte()
            bmp.copyPixelsFromBuffer(java.nio.ByteBuffer.wrap(a))
            return Img(bmp, pad).also { cache.put(key, it) }
        }

        private fun boxH(p: IntArray, w: Int, h: Int, r: Int) {
            val row = IntArray(w); val d = 2 * r + 1
            for (y in 0 until h) {
                val o = y * w; var sum = 0
                for (i in -r..r) sum += p[o + i.coerceIn(0, w - 1)]
                for (x in 0 until w) {
                    row[x] = sum / d
                    sum += p[o + (x + r + 1).coerceAtMost(w - 1)] - p[o + (x - r).coerceAtLeast(0)]
                }
                System.arraycopy(row, 0, p, o, w)
            }
        }

        private fun boxV(p: IntArray, w: Int, h: Int, r: Int) {
            val col = IntArray(h); val d = 2 * r + 1
            for (x in 0 until w) {
                var sum = 0
                for (i in -r..r) sum += p[i.coerceIn(0, h - 1) * w + x]
                for (y in 0 until h) {
                    col[y] = sum / d
                    sum += p[(y + r + 1).coerceAtMost(h - 1) * w + x] - p[(y - r).coerceAtLeast(0) * w + x]
                }
                for (y in 0 until h) p[y * w + x] = col[y]
            }
        }
    }
}
