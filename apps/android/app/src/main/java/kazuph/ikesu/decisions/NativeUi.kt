package kazuph.ikesu.decisions

import org.json.JSONObject
import java.io.ByteArrayOutputStream

/** Finds ikesu's "menu or dialog is open" signals (ESC ] 7767 ; ikesu-ui ; <base64 JSON> BEL) in the screen's bytes.
 *  A signal may be split across chunks. The signals are cut out of the bytes the terminal draws: a long one would
 *  overflow the terminal's OSC limit and be drawn as text. A partial prefix at a chunk's end waits for the next chunk. */
class NativeUiScanner {
    class Result(val signals: List<JSONObject>, val screen: ByteArray)

    private val prefix = "\u001b]7767;ikesu-ui;".toByteArray()
    private val pending = ByteArrayOutputStream()
    private var inside = false
    private var dropping = false
    private var matched = 0

    fun scan(bytes: ByteArray): Result {
        val found = mutableListOf<JSONObject>()
        val screen = ByteArrayOutputStream(bytes.size)
        for (b in bytes) {
            if (inside) {
                if (b.toInt() == 7) {
                    inside = false
                    if (!dropping) runCatching { JSONObject(String(java.util.Base64.getMimeDecoder().decode(pending.toByteArray()), Charsets.UTF_8)) }.getOrNull()?.let(found::add)
                    dropping = false
                    pending.reset()
                } else if (!dropping) {
                    if (pending.size() < MAX) pending.write(b.toInt()) else { dropping = true; pending.reset() }
                }
                continue
            }
            if (b == prefix[matched]) {
                matched++
                if (matched == prefix.size) { inside = true; matched = 0; pending.reset() }
                continue
            }
            if (matched > 0) {
                screen.write(prefix, 0, matched)
                matched = 0
                if (b == prefix[0]) { matched = 1; continue }
            }
            screen.write(b.toInt())
        }
        return Result(found, screen.toByteArray())
    }

    companion object { const val MAX = 1024 * 1024 }
}

/** Pane contents in the PC's rendered terminal cells. All bounds are zero-based and exclusive at the end. */
internal data class TerminalPaneRect(val left: Int, val top: Int, val right: Int, val bottom: Int) {
    fun contains(x: Int, y: Int): Boolean = x in left until right && y in top until bottom
}

internal class TerminalPaneLayout private constructor(
    private val cols: Int,
    private val rows: Int,
    private val panes: List<TerminalPaneRect>,
    val spacesOpen: Boolean?,
) {
    fun paneAt(cols: Int, rows: Int, x: Int, y: Int): TerminalPaneRect? {
        if (this.cols != cols || this.rows != rows) return null
        return panes.singleOrNull { it.contains(x, y) }
    }

    companion object {
        fun decode(signal: JSONObject): TerminalPaneLayout {
            val cols = signal.optInt("cols", -1)
            val rows = signal.optInt("rows", -1)
            val raw = signal.optJSONArray("panes")
            val panes = buildList {
                if (cols <= 0 || rows <= 0 || raw == null || raw.length().toLong() > cols.toLong() * rows) return@buildList
                for (i in 0 until raw.length()) {
                    val item = raw.optJSONObject(i) ?: continue
                    val x = item.optInt("x", -1)
                    val y = item.optInt("y", -1)
                    val w = item.optInt("w", 0)
                    val h = item.optInt("h", 0)
                    if (x in 0 until cols && y in 0 until rows && w in 1..(cols - x) && h in 1..(rows - y)) {
                        add(TerminalPaneRect(x, y, x + w, y + h))
                    }
                }
            }
            return TerminalPaneLayout(cols, rows, panes, signal.opt("spaces") as? Boolean)
        }
    }
}

/** The keys that move ikesu's menu selection toward a row and choose it (the menu's own ↑/↓ skip separators and
 *  disabled rows; Enter chooses). */
object MenuKeys {
    /** One step toward [to]: an arrow, or Enter once [from] is [to]. Choosing one step at a time and reading the
     *  next signal keeps the count right when ikesu rebuilds the list in between. */
    fun step(items: List<Pair<Boolean, Boolean>>, from: Int, to: Int): ByteArray? {
        if (to !in items.indices || items[to].first || items[to].second) return null
        if (from == to) return byteArrayOf(13)
        return (if (to > from) "\u001b[B" else "\u001b[A").toByteArray()
    }
}
