package kazuph.ikesu.decisions

import com.termux.terminal.TerminalBuffer
import com.termux.terminal.WcWidth

/** text: the pane's rows as drawn. copyText: the same rows with the program's wraps and shared indent undone, for copying. */
internal data class PressedTerminalContent(val text: String, val url: String?, val copyText: String = text)

internal object TerminalPress {
    private val paneTitle = Regex("^ +(?:ZOOM )?%[0-9]+(?: |$)")

    fun read(
        screen: TerminalBuffer, cols: Int, rows: Int, col: Int, row: Int, layout: TerminalPaneLayout? = null,
    ): PressedTerminalContent {
        if (cols <= 0 || rows <= 0 || col !in 1..cols || row !in 1..rows) return PressedTerminalContent("", null)
        val x = col - 1
        val y = row - 1
        val pane = layout?.paneAt(cols, rows, x, y)
        if (layout != null && pane == null) return PressedTerminalContent("", null)
        val lines = (0 until rows).map { y ->
            val line = screen.allocateFullLineIfNecessary(screen.externalToInternalRow(y))
            val cells = MutableList(cols) { "" }
            var offset = 0
            var x = 0
            var last = -1
            while (offset < line.spaceUsed) {
                val cp = Character.codePointAt(line.mText, offset, line.spaceUsed)
                val count = Character.charCount(cp)
                val width = WcWidth.width(cp)
                val value = String(line.mText, offset, count)
                if (width <= 0) {
                    if (last >= 0) cells[last] += value
                } else {
                    if (x >= cols) break
                    cells[x] = value
                    last = x
                    x += width
                }
                offset += count
            }
            cells
        }
        fun at(cx: Int, cy: Int) = lines[cy][cx]
        fun wall(cell: String) = cell == "│" || cell == "├" || cell == "┤" || cell == "┼"
        var frame = pane
        if (frame == null) loop@ for (top in y - 1 downTo 0) for (left in x - 1 downTo 0) {
            if (at(left, top) !in listOf("┌", "┬", "├", "┼")) continue
            for (right in x + 1 until cols) {
                if (at(right, top) !in listOf("┐", "┬", "┤", "┼")) continue
                if (!paneTitle.containsMatchIn(lines[top].subList(left + 1, right).joinToString(""))) continue
                for (bottom in y + 1 until rows) {
                    if (at(left, bottom) !in listOf("└", "┴", "├", "┼")) continue
                    if (at(right, bottom) !in listOf("┘", "┴", "┤", "┼")) continue
                    if ((left + 1 until right).any { at(it, bottom) !in listOf("─", "┬", "┴", "┼") }) continue
                    if ((top + 1 until bottom).any { !wall(at(left, it)) || !wall(at(right, it)) }) continue
                    frame = TerminalPaneRect(left + 1, top + 1, right, bottom)
                    break@loop
                }
            }
        }
        var outerSoftWrap = false
        val bounds = frame ?: run {
            val wallLeft = (x - 1 downTo 0).firstOrNull { wall(at(it, y)) }
            val wallRight = (x + 1 until cols).firstOrNull { wall(at(it, y)) }
            if (wallLeft != null && wallRight != null) {
                // Follow the same two walls, but stop at a junction or horizontal divider.
                fun enclosed(cy: Int) = at(wallLeft, cy) == "│" && at(wallRight, cy) == "│"
                var top = y
                var bottom = y + 1
                while (top > 0 && enclosed(top - 1)) top--
                while (bottom < rows && enclosed(bottom)) bottom++
                TerminalPaneRect(wallLeft + 1, top, wallRight, bottom)
            } else if (screen.getLineWrap(y) || (y > 0 && screen.getLineWrap(y - 1))) {
                // Termux marks actual screen-edge wraps, unlike pane-local wraps.
                var top = y
                var bottom = y + 1
                while (top > 0 && screen.getLineWrap(top - 1)) top--
                while (bottom < rows && screen.getLineWrap(bottom - 1)) bottom++
                outerSoftWrap = true
                TerminalPaneRect(0, top, cols, bottom)
            } else {
                val left = (x - 1 downTo 0).firstOrNull { (at(it, y).isNotEmpty() && at(it, y).isBlank()) || wall(at(it, y)) }?.plus(1) ?: 0
                val right = (x + 1 until cols).firstOrNull { (at(it, y).isNotEmpty() && at(it, y).isBlank()) || wall(at(it, y)) } ?: cols
                TerminalPaneRect(left, y, right, y + 1)
            }
        }
        val boundedPane = frame != null || (bounds.left > 0 && bounds.right < cols && wall(at(bounds.left - 1, y)) && wall(at(bounds.right, y)))
        val text = (bounds.top until bounds.bottom).joinToString("\n") { lines[it].subList(bounds.left, bounds.right).joinToString("").trimEnd() }.trimEnd('\n')
        val joined = StringBuilder()
        var pressedStart = -1
        var pressedEnd = -1
        for (cy in bounds.top until bounds.bottom) {
            if (cy > bounds.top) {
                val startsUrl = lines[cy].subList(bounds.left, bounds.right).joinToString("").let {
                    it.startsWith("https://") || it.startsWith("http://")
                }
                if ((outerSoftWrap && !screen.getLineWrap(cy - 1)) || at(bounds.right - 1, cy - 1).isBlank() || at(bounds.left, cy).isBlank() || startsUrl) joined.append('\n')
            }
            for (cx in bounds.left until bounds.right) {
                val start = joined.length
                joined.append(at(cx, cy))
                if (cx == x && cy == y) { pressedStart = start; pressedEnd = joined.length }
            }
        }
        // Some applications insert hard newlines at a fixed width smaller than the pane.
        // Require several equally wide, aligned URL-only rows and a shorter final row.
        var hardWrappedUrl: String? = null
        if (boundedPane) {
            val urlChars = Regex("^[A-Za-z0-9\\-._~:/?#\\[\\]@!$&'()*+,;=%]+$")
            for (startRow in bounds.top until bounds.bottom) {
                val raw = lines[startRow].subList(bounds.left, bounds.right).joinToString("").trimEnd()
                val indent = raw.indexOfFirst { !it.isWhitespace() }
                if (indent < 0) continue
                val first = raw.substring(indent)
                if (!(first.startsWith("https://") || first.startsWith("http://")) || !urlChars.matches(first)) continue

                // Reassemble screen-edge physical wraps before measuring hard-line widths.
                val logical = mutableListOf<String>()
                val spans = mutableListOf<Pair<Int, Int>>()
                var cy = startRow
                while (cy < bounds.bottom) {
                    val part = StringBuilder()
                    while (cy < bounds.bottom) {
                        val rowText = lines[cy].subList(bounds.left, bounds.right).joinToString("").trimEnd()
                        if (rowText.length <= indent || rowText.take(indent).any { !it.isWhitespace() }) break
                        val fragment = rowText.substring(indent)
                        if (!urlChars.matches(fragment) || (cy != startRow && (fragment.startsWith("https://") || fragment.startsWith("http://")))) break
                        part.append(fragment)
                        spans.add(cy to rowText.length)
                        cy++
                        if (rowText.length < bounds.right - bounds.left) break
                    }
                    if (part.isEmpty()) break
                    logical.add(part.toString())
                }
                if (logical.size < 3) continue
                val width = logical.first().length
                var fullRows = 1
                while (fullRows < logical.size && logical[fullRows].length == width) fullRows++
                if (fullRows < 2 || fullRows >= logical.size || logical[fullRows].length >= width) continue
                val value = logical.take(fullRows + 1).joinToString("")
                val includedRows = logical.take(fullRows + 1).sumOf { it.length }
                var consumed = 0
                val pressedInUrl = spans.any { (spanRow, rowEnd) ->
                    val length = rowEnd - indent
                    val inside = consumed < includedRows && spanRow == y && x - bounds.left in indent until rowEnd
                    consumed += length
                    inside
                }
                if (pressedInUrl) {
                    val whole = UrlFinder.find(value).singleOrNull()
                    if (whole != null && whole.first == 0 && whole.last == value.lastIndex) hardWrappedUrl = value
                }
            }
        }

        val match = if (pressedStart < 0 || pressedEnd <= pressedStart) null else
            UrlFinder.find(joined.toString()).firstOrNull { pressedStart <= it.last && pressedEnd > it.first }
        // A soft-wrapped final screen row explicitly says the content continues off-screen.
        val truncated = outerSoftWrap && bounds.bottom == rows && screen.getLineWrap(rows - 1) && match?.last == joined.lastIndex
        val ordinaryUrl = match?.let { joined.substring(it.first, it.last + 1) }
        // This specific observed flow needs all of its visible authorization fields.
        val authCandidate = hardWrappedUrl ?: ordinaryUrl
        val authPath = authCandidate?.let { Regex("^https?://[^/?#]+(/[^?#]*)?\\?").find(it)?.groupValues?.get(1) }
        val observedAuth = authCandidate != null && authPath?.endsWith("/authorize") == true &&
            Regex("(?:[?&])code=true(?:&|$)").containsMatchIn(authCandidate) &&
            Regex("(?:[?&])client_id=").containsMatchIn(authCandidate)
        val incompleteAuth = observedAuth && (
            listOf("redirect_uri", "scope", "code_challenge", "code_challenge_method", "state").any { name ->
                !Regex("(?:[?&])$name=[^&#]+").containsMatchIn(authCandidate!!)
            } || Regex("%(?![0-9A-Fa-f]{2})").containsMatchIn(authCandidate!!)
        )
        val url = if (truncated || incompleteAuth) null else hardWrappedUrl ?: ordinaryUrl
        val copyText = CopyText.unwrap((bounds.top until bounds.bottom).map { lines[it].subList(bounds.left, bounds.right).joinToString("") }, bounds.right - bounds.left)
        return PressedTerminalContent(text, url, copyText)
    }
}
