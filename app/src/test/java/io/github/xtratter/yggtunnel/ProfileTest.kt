package io.github.xtratter.yggtunnel

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ProfileTest {
    private val p = JSONObject().put("v", 1).put("name", "vpn.example.org").put("privateKey", "k").put("serverKey", "s")
        .put("serverYgg", "200::1").put("port", 51820)

    @Test fun roundTrip() {
        val back = Profile.parse(Profile.link(p))!!
        assertEquals("vpn.example.org", back.getString("name"))
        assertEquals(51820, back.getInt("port"))
    }

    @Test fun foundInsideAMessage() =
        assertEquals("s", Profile.parse("Here: ${Profile.link(p)} — import it")!!.getString("serverKey"))

    @Test fun rejectsGarbageAndOtherVersions() {
        assertNull(Profile.parse("no link here"))
        assertNull(Profile.parse("yggtunnel://import#!!!"))
        assertNull(Profile.parse(Profile.link(JSONObject(p.toString()).put("v", 2))))
    }
}
