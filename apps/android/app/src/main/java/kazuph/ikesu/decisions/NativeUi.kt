package kazuph.ikesu.decisions

import org.json.JSONObject
import java.io.ByteArrayOutputStream

/** Finds ikesu's "menu or dialog is open" signals (ESC ] 7767 ; ikesu-ui ; <base64 JSON> BEL) in the screen's bytes.
 *  A signal may be split across chunks; the bytes themselves still go to the terminal (it ignores unknown OSC). */
class NativeUiScanner {
    private val prefix = "\u001b]7767;ikesu-ui;".toByteArray()
    private val pending = ByteArrayOutputStream()
    private var inside = false
    private var matched = 0

    fun scan(bytes: ByteArray): List<JSONObject> {
        val found = mutableListOf<JSONObject>()
        for (b in bytes) {
            if (inside) {
                if (b.toInt() == 7) {
                    inside = false
                    runCatching { JSONObject(String(java.util.Base64.getMimeDecoder().decode(pending.toByteArray()), Charsets.UTF_8)) }.getOrNull()?.let(found::add)
                    pending.reset()
                } else if (pending.size() < MAX) pending.write(b.toInt()) else { inside = false; pending.reset() }
                continue
            }
            matched = when {
                b == prefix[matched] -> matched + 1
                b == prefix[0] -> 1
                else -> 0
            }
            if (matched == prefix.size) { inside = true; matched = 0; pending.reset() }
        }
        return found
    }

    companion object { const val MAX = 1024 * 1024 }
}

/** The keys that move ikesu's menu selection from [from] to [to] and choose it (the menu's own ↑/↓ skip separators and
 *  disabled rows; Enter chooses). Returns null when [to] cannot be chosen. */
object MenuKeys {
    fun choose(items: List<Pair<Boolean, Boolean>>, from: Int, to: Int): ByteArray? {
        fun selectable(i: Int) = i in items.indices && !items[i].first && !items[i].second
        if (!selectable(to)) return null
        val out = ByteArrayOutputStream()
        var cur = from
        var guard = 0
        while (cur != to && guard++ < items.size + 2) {
            val dir = if (to > cur) 1 else -1
            var next = cur + dir
            while (next in items.indices && !selectable(next)) next += dir
            if (!selectable(next)) return null
            out.write((if (dir > 0) "\u001b[B" else "\u001b[A").toByteArray())
            cur = next
        }
        if (cur != to) return null
        out.write(13)
        return out.toByteArray()
    }
}
