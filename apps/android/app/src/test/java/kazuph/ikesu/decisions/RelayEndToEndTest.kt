package kazuph.ikesu.decisions

import com.southernstorm.noise.protocol.Noise
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.security.KeyPairGenerator
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/** End to end through the real relay: the app's classes talk to a bridge registered with
 *  `main.mjs devices add`. Runs only when IKESU_E2E_ADD names a command that registers the
 *  keys and prints the connection line (for example an ssh to the PC running the fixture).
 *  The operate key is a software P-256 key here; on the phone it lives in the Keystore. */
private val B64Std = java.util.Base64.getEncoder().let { encoder -> object { fun encodeToString(b: ByteArray): String = encoder.encodeToString(b); fun decode(s: String): ByteArray = java.util.Base64.getDecoder().decode(s) } }

class RelayEndToEndTest {
    @Test fun registeredPhoneSeesTheDecisionUnlocksAndAnswers() {
        val add = System.getenv("IKESU_E2E_ADD")
        assumeTrue("IKESU_E2E_ADD is not set", !add.isNullOrEmpty())
        val dh = Noise.createDH("25519").apply { generateKeyPair() }
        val noisePrivate = ByteArray(32).also { dh.getPrivateKey(it, 0) }
        val noisePublic = ByteArray(32).also { dh.getPublicKey(it, 0) }
        val operate = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()
        val command = "$add --name e2e-phone --noise ${B64.encode(noisePublic)} --operate ${B64.encode(operate.public.encoded)}"
        val process = ProcessBuilder("sh", "-c", command).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        assertEquals(output, 0, process.waitFor())
        val machine = ManualLink.parse(output.lines().last { it.startsWith("{") })

        val events = LinkedBlockingQueue<Pair<String, JSONObject?>>()
        val session = NoiseSession(noisePrivate, B64.decode(machine.bridge), B64.decode(machine.room))
        val link = RelayLink(RelayLink.appUrl(machine), machine.tag, session, object : RelayLink.Listener {
            override fun onReady(link: RelayLink) { events.put("ready" to null) }
            override fun onMessage(link: RelayLink, message: JSONObject) { events.put(message.optString("type") to message) }
            override fun onClosed(link: RelayLink, reason: String) { events.put("closed" to JSONObject().put("reason", reason)) }
        })
        link.start()
        val seen = mutableListOf<String>()
        fun next(type: String): JSONObject {
            val deadline = System.currentTimeMillis() + 20_000
            while (true) {
                val left = deadline - System.currentTimeMillis()
                val (kind, message) = events.poll(maxOf(left, 1), TimeUnit.MILLISECONDS) ?: error("timed out waiting for $type; seen $seen")
                if (kind == "closed") error("closed: ${message?.optString("reason")}")
                seen.add(kind + ":" + (message?.toString()?.take(200) ?: ""))
                if (kind == type) return message ?: JSONObject()
            }
        }
        next("ready")
        val notice = next("notice")
        val decisions = notice.getJSONArray("decisions")
        assertTrue(notice.toString(), (0 until decisions.length()).any { decisions.getJSONObject(it).optString("title").contains("外出先から答える試験") })
        assertTrue(notice.toString(), !notice.toString().contains("options"))

        // Answering before the biometric signature is refused.
        val id = decisions.getJSONObject(0).getString("id")
        link.send(JSONObject().put("type", "answer").put("id", id).put("sequence", "s0").put("options", org.json.JSONArray(listOf("yes"))).put("text", "").put("images", org.json.JSONArray()))
        assertEquals("operate required", next("error").optString("message"))

        val signer = Signature.getInstance("SHA256withECDSA").apply { initSign(operate.private) }
        signer.update(RelayMath.operateMessage(listOf(link.handshakeHash)))
        link.send(JSONObject().put("type", "operate").put("hashes", org.json.JSONArray(listOf(B64.encode(link.handshakeHash)))).put("sig", B64.encode(signer.sign())))
        next("operate_ok")
        val snapshot = next("snapshot")
        assertTrue(snapshot.toString(), snapshot.toString().contains("見えてる"))
        link.send(JSONObject().put("type", "answer").put("id", id).put("sequence", "s1").put("options", org.json.JSONArray(listOf("yes"))).put("text", "外出先から").put("images", org.json.JSONArray()))
        val result = next("answer_result")
        assertEquals("s1", result.optString("sequence"))
        assertEquals(result.toString(), "answered", result.optString("status"))

        // The whole ikesu screen at the phone's size: the PC draws it, and typed keys run in its shell (PROTOCOL 4).
        link.send(JSONObject().put("type", "screen_open").put("cols", 44).put("rows", 20))
        val screen = next("screen_opened").getInt("screen")
        var drawn = ""
        fun drawnUntil(text: String) {
            while (!drawn.contains(text)) {
                val data = next("screen_data")
                if (data.getInt("screen") == screen) drawn += String(B64Std.decode(data.getString("data")), Charsets.UTF_8).replace(Regex("\u001b\\[[0-9;?]*[A-Za-z]"), "")
            }
        }
        drawnUntil("ikesu")
        val beat = next("beat").getLong("n")
        link.send(JSONObject().put("type", "screen_input").put("screen", screen).put("beat", beat)
            .put("data", B64Std.encodeToString("echo phone-$((6*7))\r".toByteArray())))
        drawnUntil("phone-42")
        link.send(JSONObject().put("type", "screen_close").put("screen", screen))
        assertEquals(screen, next("screen_closed").getInt("screen"))
        link.close("試験の終わり")
    }
}
