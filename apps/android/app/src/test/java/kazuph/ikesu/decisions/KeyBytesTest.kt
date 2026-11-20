package kazuph.ikesu.decisions

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test

/** The bytes the screen's keys send to ikesu: control letters, ⌘ as the kitty super bit, mouse clicks, paste. */
class KeyBytesTest {
    private fun s(bytes: ByteArray) = String(bytes, Charsets.UTF_8)

    @Test fun plainTextGoesAsIs() = assertEquals("ls", s(KeyBytes.text("ls", 0)))

    @Test fun ctrlTurnsLettersIntoControlBytes() {
        assertArrayEquals(byteArrayOf(3), KeyBytes.text("c", KeyBytes.CTRL))
        assertArrayEquals(byteArrayOf(20), KeyBytes.text("t", KeyBytes.CTRL))
        assertArrayEquals(byteArrayOf(0), KeyBytes.text(" ", KeyBytes.CTRL))
    }

    @Test fun shiftAndAlt() {
        assertEquals("A", s(KeyBytes.text("a", KeyBytes.SHIFT)))
        assertEquals("\u001bx", s(KeyBytes.text("x", KeyBytes.ALT)))
    }

    @Test fun commandIsSentAsKittySuper() {
        assertEquals("\u001b[118;9u", s(KeyBytes.text("v", KeyBytes.SUPER)))
        assertEquals("\u001b[99;13u", s(KeyBytes.text("c", KeyBytes.SUPER or KeyBytes.CTRL)))
        assertEquals("\u001b[1;9A", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_DPAD_UP, KeyBytes.SUPER, false)!!))
    }

    @Test fun clickIsAnSgrPressAndRelease() = assertEquals("\u001b[<0;5;7M\u001b[<0;5;7m", s(KeyBytes.click(5, 7)))

    @Test fun wheelIsAnSgrPress() {
        assertEquals("\u001b[<64;5;7M", s(KeyBytes.wheel(5, 7, true)))
        assertEquals("\u001b[<65;5;7M", s(KeyBytes.wheel(5, 7, false)))
    }

    @Test fun pasteIsBracketedAndDropsEscapes() = assertEquals("\u001b[200~ab\u001b[201~", s(KeyBytes.paste("a\u001bb")))
    @Test fun controlTabKeepsItsModifier() {
        assertEquals("\u001b[9;5u", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_TAB, KeyBytes.CTRL, false)!!))
        assertEquals("\u001b[9;6u", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_TAB, KeyBytes.CTRL or KeyBytes.SHIFT, false)!!))
        assertEquals("\t", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_TAB, 0, false)!!))
        assertEquals("\u001b[Z", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_TAB, KeyBytes.SHIFT, false)!!))
    }
    @Test fun hardwareModifierStateFeedsControlTab() {
        val meta = android.view.KeyEvent.META_CTRL_ON or android.view.KeyEvent.META_SHIFT_ON
        val mods = KeyBytes.fromMetaState(meta)
        assertEquals(KeyBytes.CTRL or KeyBytes.SHIFT, mods)
        assertEquals("\u001b[9;6u", s(KeyBytes.code(android.view.KeyEvent.KEYCODE_TAB, mods, false)!!))
    }
}
