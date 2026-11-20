package kazuph.ikesu.decisions

import com.termux.terminal.TerminalBuffer
import com.termux.terminal.TerminalEmulator
import com.termux.terminal.TerminalOutput
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.json.JSONObject

class TerminalPressTest {
    private fun screen(vararg lines: String): TerminalBuffer {
        val width = lines.maxOf { it.length }
        val buffer = TerminalBuffer(width, lines.size, lines.size)
        lines.forEachIndexed { y, line -> line.forEachIndexed { x, c -> buffer.setChar(x, y, c.code, 0) } }
        return buffer
    }

    @Test fun paneIsolationAndUrls() {
        val lines = arrayOf("┌ %1 left ───────┐┌ %2 right ──────┐", "│https://a.co/x  ││https://b.co/y  │", "│more text       ││other           │", "└────────────────┘└────────────────┘")
        val b = screen(*lines)
        val result = TerminalPress.read(b, lines[0].length, lines.size, 5, 2)
        assertEquals("https://a.co/x\nmore text", result.text)
        assertEquals("https://a.co/x", result.url)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 3).url)
    }

    @Test fun wrappedUrls() {
        val lines = arrayOf("┌ %1 test ───────────┐", "│https://one.co  abc │", "│https://example.com/│", "│path?q=1            │", "└────────────────────┘")
        val b = screen(*lines)
        assertEquals("https://example.com/path?q=1", TerminalPress.read(b, lines[0].length, lines.size, 3, 4).url)
        assertEquals("https://one.co", TerminalPress.read(b, lines[0].length, lines.size, 4, 2).url)
    }
    @Test fun separateUrlsAndSafeFallback() {
        val lines = arrayOf("https://a.co https://b.co")
        val b = screen(*lines)
        assertEquals("https://a.co", TerminalPress.read(b, lines[0].length, 1, 3, 1).url)
        assertEquals("https://b.co", TerminalPress.read(b, lines[0].length, 1, 18, 1).url)
        assertNull(TerminalPress.read(b, lines[0].length, 1, 13, 1).url)
        assertEquals("https://b.co", TerminalPress.read(b, lines[0].length, 1, 18, 1).text)
    }

    @Test fun wideAndCombiningCells() {
        val b = TerminalBuffer(12, 1, 1)
        b.setChar(0, 0, '漢'.code, 0)
        b.setChar(2, 0, 0x1f600, 0)
        b.setChar(4, 0, 'e'.code, 0)
        b.setChar(4, 0, 0x301, 0)
        assertEquals("漢😀é", TerminalPress.read(b, 12, 1, 1, 1).text)
    }
    @Test fun multipleUrlsInOnePaneLine() {
        val lines = arrayOf("┌ %1 test ────────────────────┐", "│https://a.co https://b.co    │", "└─────────────────────────────┘")
        val b = screen(*lines)
        assertEquals("https://a.co", TerminalPress.read(b, lines[0].length, 3, 4, 2).url)
        assertEquals("https://b.co", TerminalPress.read(b, lines[0].length, 3, 19, 2).url)
        assertNull(TerminalPress.read(b, lines[0].length, 3, 14, 2).url)
    }

    @Test fun sharedBordersAndWrappedLink() {
        val leftWidth = 20
        val rightWidth = 17
        fun header(title: String, width: Int) = " %$title ".padEnd(width, '─')
        val lines = arrayOf(
            "┌" + header("1 left", leftWidth) + "┬" + header("2 right", rightWidth) + "┐",
            "│" + "https://example.com/" + "│" + "other pane".padEnd(rightWidth) + "│",
            "│" + "tail?q=1".padEnd(leftWidth) + "│" + "other row".padEnd(rightWidth) + "│",
            "│" + "ordinary text".padEnd(leftWidth) + "│" + "other text".padEnd(rightWidth) + "│",
            "└" + "─".repeat(leftWidth) + "┴" + "─".repeat(rightWidth) + "┘",
        )
        val b = screen(*lines)
        val result = TerminalPress.read(b, lines[0].length, lines.size, 3, 3)
        assertEquals("https://example.com/tail?q=1", result.url)
        assertEquals("https://example.com/\ntail?q=1\nordinary text", result.text)
        assertEquals("other pane\nother row\nother text", TerminalPress.read(b, lines[0].length, lines.size, 25, 2).text)
    }
    private fun longUrl(): String = "https://example.test/?data=" + "%AB".repeat(164)

    @Test fun eightRowsWithMissingHeaderAndSharedWall() {
        val url = longUrl()
        val parts = url.chunked(70)
        assertEquals(8, parts.size)
        val lines = parts.mapIndexed { index, part ->
            "│" + part.padEnd(70) + "│" + "neighbor $index".padEnd(18) + "│"
        }.toTypedArray()
        val b = screen(*lines)
        for (row in listOf(4, 8)) {
            val result = TerminalPress.read(b, lines[0].length, lines.size, 3, row)
            assertEquals(url, result.url)
            assertEquals(parts.joinToString("\n"), result.text)
        }
        assertEquals((0..7).joinToString("\n") { "neighbor $it" },
            TerminalPress.read(b, lines[0].length, lines.size, 74, 4).text)
    }

    @Test fun eightRowsWithMissingFooter() {
        val parts = longUrl().chunked(70)
        val header = "┌" + " %33 pane ".padEnd(70, '─') + "┐"
        val lines = (listOf(header) + parts.map { "│" + it.padEnd(70) + "│" }).toTypedArray()
        val b = screen(*lines)
        assertEquals(longUrl(), TerminalPress.read(b, lines[0].length, lines.size, 3, 5).url)
        assertEquals(longUrl(), TerminalPress.read(b, lines[0].length, lines.size, 3, 9).url)
    }

    @Test fun actualScreenSoftWrapAndOffscreenEnd() {
        val parts = longUrl().chunked(70)
        val b = TerminalBuffer(70, 8, 8)
        parts.forEachIndexed { y, line ->
            line.forEachIndexed { x, c -> b.setChar(x, y, c.code, 0) }
            if (y < parts.lastIndex) b.setLineWrap(y)
        }
        assertEquals(longUrl(), TerminalPress.read(b, 70, 8, 2, 4).url)
        assertEquals(longUrl(), TerminalPress.read(b, 70, 8, 2, 8).url)
        val cropped = TerminalBuffer(70, 7, 7)
        parts.take(7).forEachIndexed { y, line ->
            line.forEachIndexed { x, c -> cropped.setChar(x, y, c.code, 0) }
            cropped.setLineWrap(y)
        }
        assertNull(TerminalPress.read(cropped, 70, 7, 2, 4).url)
    }

    @Test fun consecutiveStandaloneUrlsRemainSeparate() {
        val first = "https://example.test/" + "a".repeat(49)
        assertEquals(70, first.length)
        val second = "https://other.test/next"
        val lines = arrayOf("│" + first + "│", "│" + second.padEnd(70) + "│")
        val b = screen(*lines)
        assertEquals(first, TerminalPress.read(b, lines[0].length, 2, 4, 1).url)
        assertEquals(second, TerminalPress.read(b, lines[0].length, 2, 4, 2).url)
    }
    @Test fun emulatorProducesScreenEdgeWrap() {
        val output = object : TerminalOutput() {
            override fun write(data: ByteArray, offset: Int, count: Int) {}
            override fun titleChanged(oldTitle: String?, newTitle: String?) {}
            override fun onCopyTextToClipboard(text: String?) {}
            override fun onPasteTextFromClipboard() {}
            override fun onBell() {}
            override fun onColorsChanged() {}
        }
        val emulator = TerminalEmulator(output, 70, 8, 8, 16, 2000, null)
        val bytes = longUrl().toByteArray(Charsets.UTF_8)
        emulator.append(bytes, bytes.size)
        assertEquals(longUrl(), TerminalPress.read(emulator.screen, 70, 8, 2, 4).url)
        assertEquals(longUrl(), TerminalPress.read(emulator.screen, 70, 8, 2, 8).url)
    }
    @Test fun completeFrameEightRowsStillReturnsWholePane() {
        val parts = longUrl().chunked(70)
        val lines = (listOf("┌" + " %33 pane ".padEnd(70, '─') + "┐") +
            parts.map { "│" + it.padEnd(70) + "│" } + listOf("└" + "─".repeat(70) + "┘")).toTypedArray()
        val b = screen(*lines)
        val result = TerminalPress.read(b, 72, lines.size, 2, 9)
        assertEquals(longUrl(), result.url)
        assertEquals(parts.joinToString("\n"), result.text)
    }
    private fun authorizationUrl(): String {
        val prefix = "https://auth.example.test/oauth/cli/authorize?code=true&client_id=".padEnd(70, 'x')
        val middle = "&redirect_uri=https%3A%2F%2Fclient.example.test%2Fcallback%3Fa%3D1%26b%3D2" +
            "&scope=openid%20profile&code_challenge=" + "Z".repeat(43) +
            "&code_challenge_method=S256&state="
        return (prefix + middle).padEnd(497, 's')
    }

    private fun wideFrame(content: List<String>): Array<String> =
        (listOf("┌" + " %33 auth ".padEnd(90, '─') + "┬" + " %34 next ".padEnd(20, '─') + "┐") +
            content.map { "│" + it.padEnd(90) + "│" + "neighbor.example".padEnd(20) + "│" } +
            listOf("└" + "─".repeat(90) + "┴" + "─".repeat(20) + "┘")).toTypedArray()

    @Test fun hardNewlinesReconstructAllAuthorizationParameters() {
        val url = authorizationUrl()
        val pieces = url.chunked(70)
        assertEquals(listOf(70, 70, 70, 70, 70, 70, 70, 7), pieces.map { it.length })
        val lines = wideFrame(pieces)
        val b = screen(*lines)
        for (row in listOf(2, 5, 9)) {
            assertEquals(url, TerminalPress.read(b, lines[0].length, lines.size, 2, row).url)
        }
        assertEquals(url, TerminalPress.read(b, lines[0].length, lines.size, 8, 9).url)
        assertEquals(null, TerminalPress.read(b, lines[0].length, lines.size, 94, 4).url)
        assertEquals(null, TerminalPress.read(b, lines[0].length, lines.size, 80, 4).url)
    }

    @Test fun authorizationFirstLineAloneIsNotOpened() {
        val lines = wideFrame(listOf(authorizationUrl().take(70), ""))
        val b = screen(*lines)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
    }

    @Test fun unrelatedProseAndSeparateUrlsAreNotAppended() {
        val url = "https://example.test/normal?x=1"
        val lines = wideFrame(listOf(url, "ordinary prose follows here", "https://other.test/", "another unrelated sentence"))
        val b = screen(*lines)
        assertEquals(url, TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
        assertEquals("https://other.test/", TerminalPress.read(b, lines[0].length, lines.size, 3, 4).url)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 3).url)
    }

    @Test fun ordinaryAuthorizationLinkWithoutRedirectIsAllowed() {
        val url = "https://auth.example.test/oauth/cli/authorize?response_type=code&client_id=demo"
        val lines = wideFrame(listOf(url, ""))
        val b = screen(*lines)
        assertEquals(url, TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
    }
    @Test fun incompleteAuthorizationWithShortVisibleContinuationIsNotOpened() {
        val prefix = authorizationUrl().take(70)
        val lines = wideFrame(listOf(prefix, "x".repeat(70), "tail"))
        val b = screen(*lines)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 4).url)
    }

    @Test fun twoFullRowsThenOrdinarySentenceDoNotJoin() {
        val first = "https://example.test/" + "x".repeat(49)
        val lines = wideFrame(listOf(first, "a".repeat(70), "ordinary prose here"))
        val b = screen(*lines)
        assertEquals(first, TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 4).url)
    }
    @Test fun narrowPaneReflowsEachHardLineAndKeepsPressedCells() {
        val parts = authorizationUrl().chunked(70)
        val physical = parts.flatMap { it.chunked(42) }
        val lines = (listOf("┌" + " %33 auth ".padEnd(42, '─') + "┐") +
            physical.map { "│" + it.padEnd(42) + "│" } + listOf("└" + "─".repeat(42) + "┘")).toTypedArray()
        val b = screen(*lines)
        for (row in listOf(2, 3, 8, 15, 16)) {
            assertEquals(authorizationUrl(), TerminalPress.read(b, 44, lines.size, 2, row).url)
        }
    }

    @Test fun missingEndAfterRedirectDoesNotOpenKnownFlow() {
        val partial = authorizationUrl().chunked(70).take(4)
        val lines = wideFrame(partial)
        val b = screen(*lines)
        for (row in listOf(2, 4, 5)) assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, row).url)
        val shortPartial = authorizationUrl().substringBefore("&state=") + "&state="
        val partialLines = wideFrame(shortPartial.chunked(70))
        val partialBuffer = screen(*partialLines)
        assertNull(TerminalPress.read(partialBuffer, partialLines[0].length, partialLines.size, 3, 2).url)
    }

    @Test fun knownFlowGuardDoesNotDependOnLineWidth() {
        val first = authorizationUrl().take(68)
        val lines = wideFrame(listOf(first, ""))
        val b = screen(*lines)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
    }
    @Test fun incompleteEncodedSuffixIsNotOpened() {
        val partial = authorizationUrl().take(490) + "%A"
        val lines = wideFrame(partial.chunked(70))
        val b = screen(*lines)
        assertNull(TerminalPress.read(b, lines[0].length, lines.size, 3, 2).url)
    }

    @Test fun differentHardLineWidthAlsoReconstructs() {
        val lines = wideFrame(authorizationUrl().chunked(58))
        val b = screen(*lines)
        assertEquals(authorizationUrl(), TerminalPress.read(b, lines[0].length, lines.size, 3, 3).url)
    }
    @Test fun knownFlowGuardDoesNotApplyToOtherPathsOrCodeValues() {
        val urls = listOf("https://auth.example.test/oauth/cli/notauthorize?code=true&client_id=demo",
            "https://auth.example.test/oauth/cli/authorize?code=false&client_id=demo")
        val lines = wideFrame(urls)
        val b = screen(*lines)
        urls.forEachIndexed { i, url ->
            assertEquals(url, TerminalPress.read(b, lines[0].length, lines.size, 3, i + 2).url)
        }
    }

    @Test fun pcPaneLayoutFindsAVisibleLinkWithoutBorders() {
        val width = 54
        val url = authorizationUrl()
        val pieces = url.chunked(width)
        val lines = (listOf("outside the pane".padEnd(width) + "another") +
            pieces.map { it.padEnd(width) + "neighbor" } +
            listOf("ordinary text".padEnd(width) + "neighbor")).toTypedArray()
        val cols = lines.maxOf { it.length }
        val rows = lines.size
        val b = screen(*lines)
        val layout = TerminalPaneLayout.decode(JSONObject("""
            {"kind":"panes","cols":$cols,"rows":$rows,"panes":[
                {"id":33,"x":0,"y":1,"w":$width,"h":${rows - 1}},
                {"id":34,"x":$width,"y":1,"w":8,"h":${rows - 1}}]}
        """.trimIndent()))

        assertNull(TerminalPress.read(b, cols, rows, 7, 5).url)
        for (row in listOf(2, 5, pieces.size + 1)) {
            val result = TerminalPress.read(b, cols, rows, 7, row, layout)
            assertEquals(url, result.url)
            assertEquals((pieces + "ordinary text").joinToString("\n"), result.text)
        }
        assertNull(TerminalPress.read(b, cols, rows, 7, 1, layout).url)
        assertEquals("", TerminalPress.read(b, cols, rows, 7, 1, layout).text)
        assertEquals("", TerminalPress.read(b, cols, rows - 1, 7, 5, layout).text)
        assertNull(TerminalPress.read(b, cols, rows, width + 2, 5, layout).url)
    }
}
