package io.github.xtratter.yggtunnel

import android.view.View
import io.github.xtratter.uikit.Help

/** Long-press help bubble (android-ui-kit Help, as in AppShelf): `view.help(R.string.h_x_t, R.string.h_x)`. */
fun <V : View> V.help(title: Int, text: Int): V = apply { Help.attach(this, title, text) }
