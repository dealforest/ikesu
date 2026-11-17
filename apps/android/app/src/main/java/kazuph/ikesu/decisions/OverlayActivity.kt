package kazuph.ikesu.decisions

/** The decisions and settings laid over the PC's screen. The screen underneath stays open and keeps drawing, so
 *  closing this panel shows it again at once. With no screen open, the request goes to MainActivity instead. */
class OverlayActivity : MainActivity() {
    override val overScreen = true
}
