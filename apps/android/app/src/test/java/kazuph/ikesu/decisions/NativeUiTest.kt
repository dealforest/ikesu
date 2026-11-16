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
        assertEquals(0, first.size)
        assertEquals(listOf("menu"), second.map { it.getString("kind") })
    }

    @Test fun otherEscapesAreNotSignals() = assertEquals(0, NativeUiScanner().scan("\u001b]0;title\u0007\u001b[31mred".toByteArray()).size)

    @Test fun choosingMovesOverSeparatorsAndDisabledRows() {
        val items = listOf(false to false, true to false, false to true, false to false)
        assertEquals("\u001b[B\r", String(MenuKeys.choose(items, 0, 3)!!))
        assertEquals("\u001b[A\r", String(MenuKeys.choose(items, 3, 0)!!))
        assertEquals("\r", String(MenuKeys.choose(items, 3, 3)!!))
        assertEquals("\u001b[B\r", String(MenuKeys.choose(items, -1, 0)!!))
        assertNull(MenuKeys.choose(items, 0, 2))
    }
}
