package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** ikesu's menu signal is found even when split across chunks, and choosing an item gives the keys ikesu's menu reads. */
class NativeUiTest {
    private fun signal(json: String) = "\u001b]7767;ikesu-ui;" + java.util.Base64.getEncoder().encodeToString(json.toByteArray()) + "\u0007"

    @Test fun findsASignalSplitAcrossChunks() {
        val scanner = NativeUiScanner()
        val bytes = ("abc" + signal("""{"kind":"menu","sel":0}""") + "def").toByteArray()
        val first = scanner.scan(bytes.copyOfRange(0, 9))
        val second = scanner.scan(bytes.copyOfRange(9, bytes.size))
        assertEquals(0, first.signals.size)
        assertEquals(listOf("menu"), second.signals.map { it.getString("kind") })
        // The terminal draws everything but the signal, even when it was split.
        assertEquals("abcdef", String(first.screen) + String(second.screen))
    }

    @Test fun otherEscapesAreNotSignals() {
        val text = "\u001b]0;title\u0007\u001b[31mred\u001b]77x"
        val result = NativeUiScanner().scan(text.toByteArray())
        assertEquals(0, result.signals.size)
        assertEquals(text, String(result.screen))
    }

    @Test fun anOversizedSignalIsDroppedWithoutReachingTheScreen() {
        val result = NativeUiScanner().scan(("a\u001b]7767;ikesu-ui;" + "A".repeat(NativeUiScanner.MAX + 10) + "\u0007b").toByteArray())
        assertEquals(0, result.signals.size)
        assertEquals("ab", String(result.screen))
    }

    @Test fun choosingStepsOverSeparatorsAndDisabledRows() {
        val items = listOf(false to false, true to false, false to true, false to false)
        assertEquals("\u001b[B", String(MenuKeys.step(items, 0, 3)!!))
        assertEquals("\u001b[A", String(MenuKeys.step(items, 3, 0)!!))
        assertEquals("\r", String(MenuKeys.step(items, 3, 3)!!))
        assertEquals("\u001b[B", String(MenuKeys.step(items, -1, 0)!!))
        assertNull(MenuKeys.step(items, 0, 2))
        assertNull(MenuKeys.step(items, 0, 1))
    }
}
