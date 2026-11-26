package kazuph.ikesu.decisions

/**
 * Turns the rows of a pane, as drawn, back into the text the program wrote for copying.
 * AI tools (Claude Code, Codex) break long sentences into rows of the pane's width and indent
 * every row, so the rows copied as they are carry line breaks in the middle of sentences and
 * spaces at the head of each line.
 *
 * - The indent shared by every non-blank row goes away.
 * - A row joins the next one when the next row's first word would not have fitted after it
 *   (the program wrapped there). A blank row, a list item and a deeper or shallower indent
 *   keep their line break.
 * - Rows joined between Latin words get a space back; rows joined inside Japanese do not.
 */
object CopyText {
    private val listItem = Regex("^(?:[-*+>•・▼▶■□◆●○✓✔☐☒⏺⎿]|\\d+[.)]|[A-Za-z][.)])(?:\\s|$)")
    /** Rows of a table, a box or a code fence keep every line break. */
    private val structured = Regex("[|│┃┆┊─━┌┐└┘├┤┬┴┼]|^\\s*```")
    private val urlTail = Regex("[a-z][a-z0-9+.-]*://\\S*$", RegexOption.IGNORE_CASE)

    /** Rows joined inside a URL get no space: the URL was cut by the width, not by a space. */
    private fun joinSpace(prev: String, body: String): Boolean {
        val last = prev.trimEnd().lastOrNull() ?: return false
        return needsSpace(last, body.first()) && !urlTail.containsMatchIn(prev.trimEnd())
    }

    fun unwrap(rows: List<String>, width: Int): String {
        val lines = rows.map { it.trimEnd() }
        val shared = lines.filter { it.isNotBlank() }.minOfOrNull { indentOf(it) } ?: 0
        val out = StringBuilder()
        var previous: String? = null
        var inFence = false
        for (raw in lines) {
            val line = if (raw.isBlank()) "" else raw.substring(minOf(shared, raw.length))
            val prev = previous
            val fence = raw.trimStart().startsWith("```")
            // Inside a code fence every row stays as it is.
            if (prev != null && !inFence && !fence && wrapped(prev, raw, width)) {
                val body = line.trimStart()
                // The line built so far, not just the last row, says whether a URL is still going on.
                val built = out.substring(out.lastIndexOf('\n') + 1)
                if (built.isNotEmpty() && joinSpace(built, body)) out.append(' ')
                out.append(body)
            } else {
                if (prev != null) out.append('\n')
                out.append(line)
            }
            if (fence) inFence = !inFence
            previous = raw
        }
        return out.toString().trimEnd('\n')
    }

    /** Whether [next] continues [row]: its first word did not fit on [row] within [width] columns. */
    private fun wrapped(row: String, next: String, width: Int): Boolean {
        if (width <= 0 || row.isBlank() || next.isBlank()) return false
        val body = next.trimStart()
        if (listItem.containsMatchIn(body) || structured.containsMatchIn(row) || structured.containsMatchIn(next)) return false
        // A continuation keeps the paragraph's indent; a list item's text is indented under its marker.
        val rowIndent = indentOf(row)
        val rowText = row.trimStart()
        val paragraphIndent = if (listItem.containsMatchIn(rowText)) rowIndent + (listItem.find(rowText)?.value?.length ?: 0) else rowIndent
        val nextIndent = indentOf(next)
        if (nextIndent != rowIndent && nextIndent != paragraphIndent) return false
        val firstWord = if (body.first().code < 0x80) body.takeWhile { it != ' ' } else body.take(1)
        val gap = if (joinSpace(row, body)) 1 else 0
        return displayWidth(row) + gap + displayWidth(firstWord) > width
    }

    private fun needsSpace(before: Char, after: Char): Boolean = before.code < 0x80 && after.code < 0x80 &&
        !before.isWhitespace() && !after.isWhitespace()

    private fun indentOf(line: String): Int = line.indexOfFirst { it != ' ' }.let { if (it < 0) line.length else it }

    private fun displayWidth(text: String): Int {
        var width = 0
        var offset = 0
        while (offset < text.length) {
            val cp = text.codePointAt(offset)
            width += maxOf(0, com.termux.terminal.WcWidth.width(cp))
            offset += Character.charCount(cp)
        }
        return width
    }
}
