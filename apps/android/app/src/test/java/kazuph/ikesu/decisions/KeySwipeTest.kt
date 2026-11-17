package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class KeySwipeTest {
    @Test fun quickSwipeTypesTheAlternativeOnceHoweverManyMovesFollow() {
        // 2026-11-17: ESC を上へはじくと、指が動くたびに exit を打ち直し、6〜7回の exit で Claude Code が閉じた。
        val swipe = KeySwipe(hasAlternative = true, threshold = 24f)
        swipe.down(500f)
        var presses = 0
        for (y in listOf(495f, 480f, 470f, 460f, 450f, 440f, 430f, 420f, 410f)) {
            if (swipe.move(y, bubbleShown = false).second == KeySwipe.Press.ALTERNATIVE) presses++
        }
        assertEquals(1, presses)
        assertNull(swipe.release(bubbleShown = false))
    }

    @Test fun tapTypesTheMainKey() {
        val swipe = KeySwipe(true, 24f)
        swipe.down(500f)
        assertNull(swipe.move(498f, false).second)
        assertEquals(KeySwipe.Press.MAIN, swipe.release(false))
    }

    @Test fun holdThenSlideUpTypesTheAlternativeOnRelease() {
        val swipe = KeySwipe(true, 24f)
        swipe.down(500f)
        val (changed, now) = swipe.move(450f, bubbleShown = true)
        assertTrue(changed); assertNull(now)
        assertNull(swipe.move(440f, true).second)
        assertEquals(KeySwipe.Press.ALTERNATIVE, swipe.release(true))
        assertNull(swipe.release(true))
    }

    @Test fun holdAndReleaseBelowTypesNothing() {
        val swipe = KeySwipe(true, 24f)
        swipe.down(500f)
        assertNull(swipe.release(bubbleShown = true))
    }

    @Test fun keyWithoutAlternativeNeverSwipes() {
        val swipe = KeySwipe(false, 24f)
        swipe.down(500f)
        assertFalse(swipe.move(100f, false).first)
        assertEquals(KeySwipe.Press.MAIN, swipe.release(false))
    }

    @Test fun cancelTypesNothing() {
        val swipe = KeySwipe(true, 24f)
        swipe.down(500f); swipe.cancel()
        assertNull(swipe.move(400f, false).second)
        assertNull(swipe.release(false))
    }

    @Test fun eachTouchStartsFresh() {
        val swipe = KeySwipe(true, 24f)
        swipe.down(500f); swipe.move(400f, false); swipe.release(false)
        swipe.down(500f)
        assertEquals(KeySwipe.Press.ALTERNATIVE, swipe.move(400f, false).second)
    }
}

class VoiceDownKeyTest {
    @Test fun downStopsRecordingThenEnterWaitsForTheText() {
        val key = VoiceDownKey()
        assertEquals(VoiceDownKey.Action.STOP_RECORDING, key.press(recording = true, transcribing = false))
        // 文字にしている間に連打しても Enter はまだ送らず、打ち終えた後に1回だけ。
        repeat(3) { assertEquals(VoiceDownKey.Action.NOTHING, key.press(recording = false, transcribing = true)) }
        assertTrue(key.textDone(typed = true))
        assertFalse(key.textDone(typed = true))
        assertEquals(VoiceDownKey.Action.ENTER, key.press(recording = false, transcribing = false))
    }

    @Test fun noEnterWhenNothingWasTyped() {
        val key = VoiceDownKey()
        key.press(true, false); key.press(false, true)
        assertFalse(key.textDone(typed = false))
    }

    @Test fun noEnterAfterTextWithoutAPressWhileTranscribing() {
        val key = VoiceDownKey()
        key.press(true, false)
        assertFalse(key.textDone(typed = true))
    }

    @Test fun downWithoutRecordingIsEnter() {
        assertEquals(VoiceDownKey.Action.ENTER, VoiceDownKey().press(false, false))
    }
}
