package io.github.xtratter.yggtunnel

import android.app.Activity
import android.app.AlertDialog
import android.content.Context
import android.view.WindowInsetsController
import io.github.xtratter.uikit.M3
import io.github.xtratter.uikit.M3Background

/** Colour theme, as in AppShelf; [SYSTEM] — dark or light like Android. Applied in place (no restart). */
enum class Theme(val title: Int, val mode: M3.Mode) {
    SYSTEM(R.string.th_system, M3.Mode.SYSTEM),
    LIGHT(R.string.th_light, M3.Mode.LIGHT),
    DARK(R.string.th_dark, M3.Mode.DARK),
    GRAPHITE(R.string.th_graphite, M3.Mode.GRAPHITE),
    AMOLED(R.string.th_amoled, M3.Mode.AMOLED);

    companion object {
        fun apply(a: Activity) {
            val p = Prefs(a)
            M3.apply(a, p.theme.mode, p.translucent)
            a.window.setBackgroundDrawable(M3Background())
            // dark or light status / navigation bar icons to match the theme, not the system.
            // decorView first: before setContentView there is no decor yet, and getInsetsController() on it
            // throws a NullPointerException (0.10 crashed on start because of this)
            a.window.decorView
            val light = if (M3.light) WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS else 0
            a.window.insetsController?.setSystemBarsAppearance(light,
                WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS)
        }

        /** A dialog builder whose standard texts follow the app theme (a dark theme on a light system and back). */
        fun dialog(ctx: Context) = AlertDialog.Builder(ctx,
            if (M3.light) android.R.style.Theme_Material_Light_Dialog_Alert else android.R.style.Theme_Material_Dialog_Alert)
    }
}
