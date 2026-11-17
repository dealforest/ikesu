package kazuph.ikesu.decisions

/**
 * One touch on an extra key that has a swipe-up alternative (ESC ↑ exit, | ↑ PGUP).
 * A tap types the main key; a swipe up types the alternative. Either is typed at most once per touch:
 * once the alternative went out, the rest of the swipe (more moves, the release) types nothing.
 */
class KeySwipe(private val hasAlternative: Boolean, private val threshold: Float) {
    enum class Press { MAIN, ALTERNATIVE }

    private var downY = 0f
    private var done = false
    /** The finger is above the threshold: the release would type the alternative. */
    var up = false
        private set

    fun down(y: Float) { downY = y; up = false; done = false }

    /** Returns whether `up` changed (to recolor the bubble), and the press to type now (a quick swipe without the bubble). */
    fun move(y: Float, bubbleShown: Boolean): Pair<Boolean, Press?> {
        if (done) return false to null
        val nowUp = hasAlternative && downY - y > threshold
        val changed = nowUp != up
        up = nowUp
        if (up && !bubbleShown) { done = true; return changed to Press.ALTERNATIVE }
        return changed to null
    }

    /** The press to type on release: nothing after a quick swipe, nothing when the bubble was shown and let go below it. */
    fun release(bubbleShown: Boolean): Press? {
        if (done) return null
        done = true
        return when {
            bubbleShown && up -> Press.ALTERNATIVE
            bubbleShown -> null
            else -> Press.MAIN
        }
    }

    fun cancel() { done = true }
}

/**
 * The volume-down key on the PC screen while speaking: a short press stops the recording and types what was said.
 * Presses while the speech is still being turned into text ask for one Enter after it is typed, so pressing
 * volume-down again and again after speaking ends with the text sent.
 */
class VoiceDownKey {
    enum class Action { STOP_RECORDING, ENTER, NOTHING }

    var enterAfterText = false
        private set

    fun press(recording: Boolean, transcribing: Boolean): Action = when {
        recording -> { enterAfterText = false; Action.STOP_RECORDING }
        transcribing -> { enterAfterText = true; Action.NOTHING }
        else -> Action.ENTER
    }

    /** The text was typed (or could not be made): whether to press Enter now. Clears the request either way. */
    fun textDone(typed: Boolean): Boolean { val enter = enterAfterText && typed; enterAfterText = false; return enter }
}

/**
 * Keys without a long-press alternative repeat while held, as Termux's extra keys do (BKSP keeps deleting).
 * Only keys that type a character or a key code repeat: modifiers, actions (paste, files, voice) and raw byte
 * keys such as ^T (ikesu's prefix) do not.
 */
object KeyRepeat {
    const val DELAY_MS = 350L
    const val INTERVAL_MS = 50L

    fun repeats(key: ExtraKey): Boolean = key.popup == null && (key.main is ScreenKey.Text || key.main is ScreenKey.Code)
}
