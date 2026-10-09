package io.github.xtratter.yggtunnel

/**
 * What the log of a server call says about the way to the server (go/core/setup.go dialSSH): when the server's own
 * address did not answer, the call went through Yggdrasil. No Android here: tested.
 */
sealed interface ServerRoute {
    data object Connected : ServerRoute
    data object VpnOff : ServerRoute
    data class Failed(val why: String) : ServerRoute

    companion object {
        /** Null when the direct address worked. */
        fun of(log: String): ServerRoute? {
            val line = log.lines().lastOrNull { it.contains("Through Yggdrasil: ") } ?: return null
            val what = line.substringAfter("Through Yggdrasil: ").trim()
            return when {
                what == "connected" -> Connected
                what == "the VPN is off" -> VpnOff
                what.startsWith("failed: ") -> Failed(what.removePrefix("failed: "))
                else -> null
            }
        }
    }
}
