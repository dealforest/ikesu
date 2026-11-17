package kazuph.ikesu.decisions

import android.app.Activity
import android.app.AlertDialog
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import android.text.InputType
import android.util.Base64
import android.view.GestureDetector
import android.view.Gravity
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import android.view.View
import android.view.WindowManager
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import com.termux.terminal.KeyHandler
import com.termux.terminal.TerminalEmulator
import com.termux.terminal.TerminalOutput
import com.termux.terminal.TerminalSession
import com.termux.terminal.TerminalSessionClient
import com.termux.view.TerminalRenderer
import org.json.JSONObject

/** Messages about the remote screen, handed from DecisionService to the open ScreenActivity on the main thread. */
object ScreenBus {
    var listener: ((source: String, message: JSONObject) -> Unit)? = null
}

/** One key of the extra-keys rows: a tap does [main]; a swipe up or a long press does [popup] (like Termux). */
data class ExtraKey(val label: String, val main: ScreenKey, val popupLabel: String? = null, val popup: ScreenKey? = null)

/** What a key does. Modifiers wait for the next key; the others send bytes right away. */
sealed class ScreenKey {
    data class Text(val text: String) : ScreenKey()
    data class Code(val keyCode: Int) : ScreenKey()
    data class Modifier(val bit: Int) : ScreenKey()
    data class Bytes(val bytes: ByteArray) : ScreenKey()
    data class Action(val run: () -> Unit) : ScreenKey()
}

/** Turns keys plus the waiting modifiers into the bytes a terminal sends (xterm and the kitty keyboard protocol). */
object KeyBytes {
    const val SHIFT = 1; const val ALT = 2; const val CTRL = 4; const val SUPER = 8

    fun text(text: String, mods: Int): ByteArray {
        if (mods == 0 || text.codePointCount(0, text.length) != 1) return text.toByteArray(Charsets.UTF_8)
        var cp = text.codePointAt(0)
        // ⌘ has no byte form of its own: send the key as CSI u with the super bit, which ikesu reads.
        if (mods and SUPER != 0) return "\u001b[${Character.toLowerCase(cp)};${1 + mods}u".toByteArray()
        if (mods and SHIFT != 0) cp = Character.toUpperCase(cp)
        var out = String(Character.toChars(cp)).toByteArray(Charsets.UTF_8)
        if (mods and CTRL != 0) {
            val c = Character.toUpperCase(cp)
            out = when {
                c in '@'.code..'_'.code -> byteArrayOf((c and 0x1f).toByte())
                cp == ' '.code -> byteArrayOf(0)
                cp == '?'.code -> byteArrayOf(0x7f)
                else -> out
            }
        }
        if (mods and ALT != 0) out = byteArrayOf(27) + out
        return out
    }

    fun code(keyCode: Int, mods: Int, cursorApp: Boolean): ByteArray? {
        var termuxMods = 0
        if (mods and SHIFT != 0) termuxMods = termuxMods or KeyHandler.KEYMOD_SHIFT
        if (mods and ALT != 0) termuxMods = termuxMods or KeyHandler.KEYMOD_ALT
        if (mods and CTRL != 0) termuxMods = termuxMods or KeyHandler.KEYMOD_CTRL
        if (mods and SUPER != 0) {
            val letter = when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> 'A'; KeyEvent.KEYCODE_DPAD_DOWN -> 'B'
                KeyEvent.KEYCODE_DPAD_RIGHT -> 'C'; KeyEvent.KEYCODE_DPAD_LEFT -> 'D'; else -> null
            }
            if (letter != null) return "\u001b[1;${1 + mods}$letter".toByteArray()
            val base = when (keyCode) { KeyEvent.KEYCODE_ENTER -> 13; KeyEvent.KEYCODE_TAB -> 9; KeyEvent.KEYCODE_DEL -> 127; KeyEvent.KEYCODE_ESCAPE -> 27; else -> return null }
            return "\u001b[$base;${1 + mods}u".toByteArray()
        }
        return KeyHandler.getCode(keyCode, termuxMods, cursorApp, false)?.toByteArray()
    }

    /** One SGR mouse click (press and release) at a 1-based cell. */
    /** One SGR mouse wheel notch (up shows older lines). ikesu scrolls its own history, or passes it to the app. */
    fun wheel(col: Int, row: Int, up: Boolean): ByteArray = "\u001b[<${if (up) 64 else 65};$col;${row}M".toByteArray()
    fun click(col: Int, row: Int, button: Int = 0): ByteArray = "\u001b[<$button;$col;${row}M\u001b[<$button;$col;${row}m".toByteArray()

    /** Text pasted as one block, so the program can tell it from typing. */
    fun paste(text: String): ByteArray = "\u001b[200~${text.replace("\u001b", "")}\u001b[201~".toByteArray(Charsets.UTF_8)
}

/** The whole ikesu screen of one PC, drawn at the phone's size (PROTOCOL 4). Keys go to the PC; the terminal's own
 *  automatic replies (queries, clipboard) are never sent. */
class ScreenActivity : Activity() {
    companion object {
        const val SOURCE = "source"
        /** A MainActivity intent to open over this screen (from a notification). */
        const val PANEL = "panel"
        /** F12: the key the PC side binds to showing and hiding the spaces list on the phone's screen. */
        private val SIDEBAR_KEY = "\u001b[24~".toByteArray()
        /** The app's first page is up: the decisions and settings (MainActivity) open as a panel over it. */
        @Volatile var running = false
        private const val RED = 0xFFE53935.toInt()
        private const val GREEN = 0xFF33FF66.toInt()
        private const val DARK_GREEN = 0xFF1E9948.toInt()
        private const val PICK_FILE = 41
        private const val MAX_FILE_BYTES = 20 * 1024 * 1024
        /** The PC refuses one screen_input over 64 KB; paste in pieces below that. */
        private const val INPUT_PIECE = 48 * 1024
    }

    /** The PC shown: the one asked for, else the one shown before, else the first that can be operated. */
    private var source: String? = null
    private fun target(): DecisionSource? = source?.let { DecisionState.sources[it] }
    /** Over the terminal while the PC's screen cannot be shown: what is happening and what to do. */
    private lateinit var cover: LinearLayout
    private lateinit var coverText: TextView
    private lateinit var coverButton: TextView
    private lateinit var view: TerminalCanvas
    private lateinit var status: TextView
    /** Top right: 🔔 and how many decisions wait. A tap goes to the decisions; the 🖥️ there comes back. */
    private lateinit var bell: TextView
    private val changes = object : android.content.BroadcastReceiver() { override fun onReceive(c: Context?, i: Intent?) { markBell(); refresh() } }
    private var started = false

    private fun pickSource(): String? {
        val all = DecisionState.sources.values
        source?.let { if (it in DecisionState.sources) return it }
        return (all.firstOrNull { it.connected && it.operable } ?: all.firstOrNull { it.connected } ?: all.firstOrNull())?.id
    }

    /** Shows the PC's screen when it can be operated, or says why not over the terminal. */
    private fun refresh() {
        if (!::cover.isInitialized) return
        source = pickSource()
        val t = target()
        val name = t?.name ?: "PC"
        fun show(message: String, action: String?, run: () -> Unit) {
            cover.visibility = View.VISIBLE; coverText.text = message
            coverButton.visibility = if (action == null) View.GONE else View.VISIBLE
            coverButton.text = action ?: ""; coverButton.setOnClickListener { run() }
            status.text = name
        }
        when {
            t == null -> show("PC が登録されていません。右上の ⚙ の設定で、PC を登録してください。", "設定を開く") { openPanel("settings") }
            !t.connected -> show("$name に接続しています…", null) {}
            !t.operable -> {
                show("$name の画面は、顔・指紋で開くと出ます。", "顔・指紋で開く") { unlock() }
                // The link came up after the app opened: ask once, as opening the app does.
                if (resumed && !askedUnlock && t.link?.ready == true) { askedUnlock = true; unlock() }
            }
            else -> {
                cover.visibility = View.GONE
                if (started && screenId < 0 && !opening && view.cols > 0) open(view.cols, view.rows)
            }
        }
    }

    /** The decisions ("list") or the settings ("settings") as a panel over this screen. */
    private fun openPanel(extra: String) = startActivity(Intent(this, MainActivity::class.java).putExtra(extra, true))

    /** Ends the PC's screen shown here (it stays on the PC side for other clients). */
    private fun dropScreen() {
        closeNative(); choosing = null; answeredInput = null; view.removeCallbacks(choiceTimeout)
        if (screenId >= 0) target()?.send(JSONObject().put("type", "screen_close").put("screen", screenId))
        screenId = -1; opening = false; wanted = null
    }

    private fun choosePc() {
        val all = DecisionState.sources.values.toList()
        if (all.size < 2) return
        AlertDialog.Builder(this).setTitle("どの PC の画面を出すか")
            .setItems(all.map { "${it.name}${if (it.connected) "" else "（未接続）"}" }.toTypedArray()) { _, i ->
                if (all[i].id == source) return@setItems
                dropScreen(); view.clear(); source = all[i].id; refresh()
            }.show()
    }

    private fun markBell() {
        if (!::bell.isInitialized) return
        val waiting = DecisionState.decisions.length()
        bell.text = if (waiting > 0) "🔔 $waiting" else "🔔"
        bell.setTextColor(if (waiting > 0) Color.WHITE else GREEN)
        bell.background = if (waiting > 0) android.graphics.drawable.GradientDrawable().apply {
            setColor(RED); cornerRadius = 6 * resources.displayMetrics.density
        } else keyBackground(false, resources.displayMetrics.density)
    }
    private var screenId = -1
    /** A screen_open is on its way; size changes wait for screen_opened instead of opening a second screen. */
    private var opening = false
    private var wanted: Pair<Int, Int>? = null
    private var mods = 0
    private val modifierViews = mutableMapOf<Int, TextView>()
    private val voice by lazy { VoiceRecorder(this) }
    private var transcribing = false
    private var micView: TextView? = null
    private var input: android.widget.EditText? = null
    /** The photo or file picker is in front: keep the PC's screen open, so the same space is still shown on return. */
    private var picking = false
    /** ikesu's menus and dialogs, shown as Android lists and dialogs (its ui.native_menus signal). */
    private var scanner = NativeUiScanner()
    private var native: AlertDialog? = null
    private var nativeShown: String? = null
    /** A menu row being chosen one step at a time (see MenuKeys.step), found again by its words in each new signal. */
    private class Choice(val title: String, val label: String, val detail: String)
    private var choosing: Choice? = null
    private var lastMenu: JSONObject? = null
    private val choiceTimeout = Runnable { if (choosing != null) { choosing = null; nativeShown = null; lastMenu?.let { showNative(it) } } }
    /** An input dialog answered with this text: its signals while ikesu types the text in are not shown again. */
    private var answeredInput: Pair<String, String>? = null
    /** A file read while the screen was (re)opening, sent once screen_opened arrives. */
    private var pendingFile: Pair<String, String>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
        running = true
        // Recordings left by a killed process are never sent.
        cacheDir.listFiles { file -> file.name.startsWith("voice") && file.name.endsWith(".m4a") }?.forEach { it.delete() }
        if (android.os.Build.VERSION.SDK_INT >= 33 && checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED)
            requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
        startForegroundService(Intent(this, DecisionService::class.java))
        source = askedSource(intent)
        source = pickSource()
        val name = target()?.name ?: "PC"
        val dp = resources.displayMetrics.density
        val screenSp = getSharedPreferences("ui", MODE_PRIVATE).getFloat("screenTextSp", 13f)
        view = TerminalCanvas(this, screenSp, keys = { bytes -> sendKeyBytes(bytes) }, onTap = { col, row -> send(KeyBytes.click(col, row)) },
            onLongPress = { col, row -> longPressMenu(col, row) }, onWheel = { col, row, up, n -> send(ByteArray(0).let { var b = it; repeat(n) { b += KeyBytes.wheel(col, row, up) }; b }) },
            onTextSize = { sp -> getSharedPreferences("ui", MODE_PRIVATE).edit().putFloat("screenTextSp", sp).apply() })
        bell = TextView(this).apply {
            textSize = 18f; gravity = Gravity.CENTER; setPadding((12 * dp).toInt(), 0, (12 * dp).toInt(), 0)
            contentDescription = "判断待ち"
            setOnClickListener { openPanel("list") }
        }
        val gear = TextView(this).apply {
            text = "⚙"; textSize = 20f; setTextColor(GREEN); gravity = Gravity.CENTER; setPadding((12 * dp).toInt(), 0, (12 * dp).toInt(), 0)
            contentDescription = "設定"; background = keyBackground(false, dp)
            setOnClickListener { openPanel("settings") }
        }
        markBell()
        status = TextView(this).apply { text = name; setOnClickListener { choosePc() }; setTextColor(GREEN); textSize = 18f; setPadding((12 * dp).toInt(), (6 * dp).toInt(), (20 * dp).toInt(), (6 * dp).toInt()) }

        // The owner's Termux layout (dotfiles/.termux/termux.properties), plus ⇧, ⌘, the ikesu prefix, voice, files and the keyboard.
        val rows = listOf(
            listOf(ExtraKey("⇧", ScreenKey.Modifier(KeyBytes.SHIFT)), ExtraKey("⌘", ScreenKey.Modifier(KeyBytes.SUPER)),
                ExtraKey("^T", ScreenKey.Bytes(byteArrayOf(20))),
                ExtraKey("貼付", ScreenKey.Action { pasteClipboard() }), ExtraKey("📎", ScreenKey.Action { pickFile() }),
                ExtraKey("🎤", ScreenKey.Action { toggleVoice() })),
            listOf(ExtraKey("ESC", ScreenKey.Code(KeyEvent.KEYCODE_ESCAPE), "exit", ScreenKey.Bytes("exit\r".toByteArray())), ExtraKey("|", ScreenKey.Text("|"), "PGUP", ScreenKey.Code(KeyEvent.KEYCODE_PAGE_UP)),
                ExtraKey("$", ScreenKey.Text("$"), "PGDN", ScreenKey.Code(KeyEvent.KEYCODE_PAGE_DOWN)), ExtraKey("/", ScreenKey.Text("/"), "clear", ScreenKey.Text("/clear")),
                ExtraKey("#", ScreenKey.Text("#")), ExtraKey("↑", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_UP), "new", ScreenKey.Bytes(byteArrayOf(20))),
                ExtraKey("@", ScreenKey.Text("@")), ExtraKey("\\", ScreenKey.Text("\\")), ExtraKey("BKSP", ScreenKey.Code(KeyEvent.KEYCODE_DEL))),
            listOf(ExtraKey("TAB", ScreenKey.Code(KeyEvent.KEYCODE_TAB), "S-TAB", ScreenKey.Bytes("\u001b[Z".toByteArray())),
                ExtraKey("CTRL", ScreenKey.Modifier(KeyBytes.CTRL), "^C", ScreenKey.Bytes(byteArrayOf(3))), ExtraKey("ALT", ScreenKey.Modifier(KeyBytes.ALT)),
                ExtraKey("!", ScreenKey.Text("!")), ExtraKey("←", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_LEFT), "prev", ScreenKey.Bytes(byteArrayOf(16))),
                ExtraKey("↓", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_DOWN)), ExtraKey("→", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_RIGHT), "next", ScreenKey.Bytes(byteArrayOf(14))),
                ExtraKey("~", ScreenKey.Text("~"), "^", ScreenKey.Text("^")), ExtraKey("⏎", ScreenKey.Code(KeyEvent.KEYCODE_ENTER), "\\⏎", ScreenKey.Bytes("\\\r".toByteArray()))),
        )
        val keys = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK); setPadding((2 * dp).toInt(), (2 * dp).toInt(), (2 * dp).toInt(), (2 * dp).toInt()) }
        // Every row spans the same 9-column grid, so the keys line up (a 6-key row gets 1.5 columns each).
        for (row in rows) {
            val line = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; weightSum = 9f }
            for (key in row) line.addView(keyView(key, dp), LinearLayout.LayoutParams(0, (40 * dp).toInt(), 9f / row.size).apply { setMargins((1.5f * dp).toInt(), (1.5f * dp).toInt(), (1.5f * dp).toInt(), (1.5f * dp).toInt()) })
            keys.addView(line, LinearLayout.LayoutParams(-1, -2))
        }
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK)
            addView(LinearLayout(this@ScreenActivity).apply {
                orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL
                // The PC's ikesu hides its spaces list on the phone (the PC side sets this up); ≡ shows and hides it (F12).
                addView(TextView(this@ScreenActivity).apply {
                    text = "≡"; textSize = 22f; setTextColor(GREEN); gravity = Gravity.CENTER
                    contentDescription = "スペース一覧"; background = keyBackground(false, dp)
                    setOnClickListener { send(SIDEBAR_KEY) }
                }, LinearLayout.LayoutParams((48 * dp).toInt(), (40 * dp).toInt()).apply { setMargins((8 * dp).toInt(), (2 * dp).toInt(), 0, (2 * dp).toInt()) })
                addView(status, LinearLayout.LayoutParams(0, -2, 1f))
                addView(bell, LinearLayout.LayoutParams(-2, (40 * dp).toInt()).apply { setMargins(0, (2 * dp).toInt(), (6 * dp).toInt(), (2 * dp).toInt()) })
                addView(gear, LinearLayout.LayoutParams(-2, (40 * dp).toInt()).apply { setMargins(0, (2 * dp).toInt(), (8 * dp).toInt(), (2 * dp).toInt()) })
            }, LinearLayout.LayoutParams(-1, -2))
            addView(FrameLayout(this@ScreenActivity).apply {
                addView(view, FrameLayout.LayoutParams(-1, -1))
                cover = LinearLayout(this@ScreenActivity).apply {
                    orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK); visibility = View.GONE
                    setPadding((20 * dp).toInt(), (40 * dp).toInt(), (20 * dp).toInt(), 0)
                    isClickable = true
                    coverText = TextView(this@ScreenActivity).apply { textSize = 22f; setTextColor(GREEN) }
                    addView(coverText, LinearLayout.LayoutParams(-1, -2))
                    coverButton = TextView(this@ScreenActivity).apply {
                        textSize = 22f; setTextColor(Color.BLACK); gravity = Gravity.CENTER
                        background = android.graphics.drawable.GradientDrawable().apply { setColor(GREEN); cornerRadius = 12 * dp }
                    }
                    addView(coverButton, LinearLayout.LayoutParams(-1, (60 * dp).toInt()).apply { topMargin = (24 * dp).toInt() })
                }
                addView(cover, FrameLayout.LayoutParams(-1, -1))
            }, LinearLayout.LayoutParams(-1, 0, 1f))
            addView(inputLine(dp), LinearLayout.LayoutParams(-1, -2).apply { setMargins((4 * dp).toInt(), (2 * dp).toInt(), (4 * dp).toInt(), (2 * dp).toInt()) })
            addView(keys, LinearLayout.LayoutParams(-1, -2))
        }
        // targetSdk 35 draws under the status bar, navigation bar and keyboard: keep the screen inside them.
        root.setOnApplyWindowInsetsListener { v, insets ->
            val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars() or android.view.WindowInsets.Type.displayCutout())
            val ime = insets.getInsets(android.view.WindowInsets.Type.ime())
            v.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
            insets
        }
        setContentView(root)
        view.onSize = { cols, rows2 -> sizeChanged(cols, rows2) }
        receivePanelRequest(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent); setIntent(intent)
        askedSource(intent)?.let { asked -> if (asked != source) { dropScreen(); view.clear(); source = asked; refresh() } }
        receivePanelRequest(intent)
    }

    /** The PC asked for by [intent]. This screen is the launcher, so any app can start it: extras it cannot read
     *  (another type, a broken parcel) are ignored instead of ending the app. */
    private fun askedSource(intent: Intent): String? = runCatching { intent.getStringExtra(SOURCE) }.getOrNull()

    /** A notification's decision opens as a panel over this screen. Only the known values are carried over to a new
     *  intent of our own: never the received one, and never anything another app put in it. */
    private fun receivePanelRequest(intent: Intent) {
        // getParcelableExtra(name, class) needs Android 13; the app runs from Android 12.
        @Suppress("DEPRECATION") val panel = runCatching { intent.getParcelableExtra<android.os.Parcelable>(PANEL) }.getOrNull() as? Intent
        runCatching { intent.removeExtra(PANEL) }
        if (panel == null) return
        val request = runCatching {
            Intent(this, MainActivity::class.java).apply {
                panel.getStringExtra("decision")?.let { putExtra("decision", it) }
                if (panel.getBooleanExtra("list", false)) putExtra("list", true)
                if (panel.getBooleanExtra("settings", false)) putExtra("settings", true)
                // The debug-only text size (TextScale.fromIntent) also survives the trip, within its range.
                if ((applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0 && panel.hasExtra("text_scale"))
                    putExtra("text_scale", TextScale.clamp(panel.getFloatExtra("text_scale", 1f)))
            }
        }.getOrNull() ?: return
        startActivity(request)
    }

    private var resumed = false
    private var askedUnlock = false
    private fun unlock() = Unlock.run(this) { m -> Toast.makeText(this, m, Toast.LENGTH_LONG).show() }
    override fun onResume() {
        super.onResume()
        resumed = true
        // Opening the app asks for a face or fingerprint before showing what the PCs sent.
        askedUnlock = DecisionState.lockedLinks().isNotEmpty()
        unlock()
    }
    override fun onPause() { resumed = false; super.onPause() }

    /** Volume keys, as on the decisions: down = Enter, down held = the decisions (or, while recording, stop and type
     *  it), up held = start or stop recording. A short up press still raises the volume. While recording, a short down
     *  press also stops and types it; down presses while it becomes text press Enter once after it is typed. */
    private val voiceDown = VoiceDownKey()
    private val volume = VolumeNav(
        up = { getSystemService(android.media.AudioManager::class.java).adjustSuggestedStreamVolume(android.media.AudioManager.ADJUST_RAISE, android.media.AudioManager.USE_DEFAULT_STREAM_TYPE, android.media.AudioManager.FLAG_SHOW_UI) },
        down = {
            when (voiceDown.press(voice.recording, transcribing)) {
                VoiceDownKey.Action.STOP_RECORDING -> toggleVoice()
                VoiceDownKey.Action.ENTER -> send(byteArrayOf(13))
                VoiceDownKey.Action.NOTHING -> {}
            }
        },
        confirm = { if (voice.recording) toggleVoice() else openPanel("list") },
        back = { toggleVoice() })
    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (volume.keyDown(keyCode, event.repeatCount == 0)) { event.startTracking(); return true }
        return super.onKeyDown(keyCode, event)
    }
    override fun onKeyLongPress(keyCode: Int, event: KeyEvent): Boolean = volume.keyLongPress(keyCode) || super.onKeyLongPress(keyCode, event)
    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean = volume.keyUp(keyCode) || super.onKeyUp(keyCode, event)

    /** A key: the main label in the middle, and its swipe-up / long-press alternative small in the top-right corner. */
    private fun keyView(key: ExtraKey, dp: Float): TextView = object : TextView(this) {
        private val corner = android.graphics.Paint(android.graphics.Paint.ANTI_ALIAS_FLAG).apply { color = DARK_GREEN; textSize = 9f * resources.displayMetrics.scaledDensity; textAlign = android.graphics.Paint.Align.RIGHT }
        override fun onDraw(canvas: Canvas) {
            super.onDraw(canvas)
            key.popupLabel?.let { canvas.drawText(it, width - 3 * dp, corner.textSize + 1 * dp, corner) }
        }
    }.apply {
        text = key.label; isSingleLine = true
        gravity = Gravity.CENTER; setTextColor(GREEN); textSize = 15f
        background = keyBackground(false, dp)
        if (key.main is ScreenKey.Modifier) modifierViews[key.main.bit] = this
        if (key.label == "🎤") micView = this
        // Long press shows a bubble above the key with what the swipe up would type; sliding up while holding types it.
        // Letting go without sliding up types nothing. A quick swipe up types it at once, as before.
        // 1回のタッチで打つのは1回だけ。上へはじいて代わりのキー（exit など）を打った後は、指が動いても離しても打たない。
        val swipe = KeySwipe(key.popup != null, 24 * dp); var bubble: android.widget.PopupWindow? = null
        fun hideBubble() { bubble?.dismiss(); bubble = null }
        fun markBubble() {
            (bubble?.contentView as? TextView)?.apply { setTextColor(if (swipe.up) Color.BLACK else GREEN); background = keyBackground(swipe.up, dp) }
        }
        val longPress = Runnable {
            if (key.popupLabel == null || bubble != null) return@Runnable
            val label = TextView(this@ScreenActivity).apply {
                text = "↑ ${key.popupLabel}"; textSize = 18f; gravity = Gravity.CENTER
                setPadding((14 * dp).toInt(), (8 * dp).toInt(), (14 * dp).toInt(), (8 * dp).toInt())
            }
            bubble = android.widget.PopupWindow(label, -2, -2, false).apply { isClippingEnabled = false }
            label.measure(0, 0)
            val at = IntArray(2); this.getLocationInWindow(at)
            bubble?.showAtLocation(this, Gravity.NO_GRAVITY, at[0] + (width - label.measuredWidth) / 2, at[1] - label.measuredHeight - (6 * dp).toInt())
            markBubble()
        }
        setOnTouchListener { v, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> { swipe.down(event.y); v.postDelayed(longPress, 350); v.isPressed = true }
                MotionEvent.ACTION_MOVE -> {
                    val (changed, now) = swipe.move(event.y, bubble != null)
                    if (changed) markBubble()
                    // A quick swipe up without waiting for the bubble types the alternative right away, once.
                    if (now != null) { v.removeCallbacks(longPress); v.isPressed = false; press(key.popup!!) }
                }
                MotionEvent.ACTION_UP -> {
                    v.removeCallbacks(longPress); v.isPressed = false
                    val shown = bubble != null
                    hideBubble()
                    when (swipe.release(shown)) {
                        KeySwipe.Press.ALTERNATIVE -> press(key.popup!!)
                        KeySwipe.Press.MAIN -> press(key.main)
                        null -> {}
                    }
                }
                MotionEvent.ACTION_CANCEL -> { swipe.cancel(); v.removeCallbacks(longPress); v.isPressed = false; hideBubble() }
            }
            true
        }
    }

    /** One input line, as in Termux: Enter sends what was typed (without Enter); Enter on an empty line sends Enter.
     *  The phone's own keyboard works here, Japanese conversion included. */
    private fun inputLine(dp: Float): android.widget.EditText = android.widget.EditText(this).apply {
        hint = "ここに打って Enter で送る（空で Enter は改行を送る）"; setHintTextColor(DARK_GREEN); setTextColor(GREEN); textSize = 16f
        isSingleLine = true; imeOptions = EditorInfo.IME_ACTION_SEND or EditorInfo.IME_FLAG_NO_FULLSCREEN
        inputType = InputType.TYPE_CLASS_TEXT
        setPadding((10 * dp).toInt(), (8 * dp).toInt(), (10 * dp).toInt(), (8 * dp).toInt())
        background = android.graphics.drawable.GradientDrawable().apply { setColor(Color.BLACK); setStroke((1.5f * dp).toInt(), DARK_GREEN); cornerRadius = 6 * dp }
        fun submit() {
            val typed = text.toString()
            if (typed.isEmpty()) KeyBytes.code(KeyEvent.KEYCODE_ENTER, takeMods(), false)?.let { send(it) }
            else { sendKeyBytes(typed.toByteArray(Charsets.UTF_8)); setText("") }
        }
        setOnEditorActionListener { _, action, event ->
            if (action == EditorInfo.IME_ACTION_SEND || action == EditorInfo.IME_ACTION_DONE || event?.keyCode == KeyEvent.KEYCODE_ENTER) {
                if (event == null || event.action == KeyEvent.ACTION_DOWN) submit()
                true
            } else false
        }
        // With ⌘ / CTRL / ALT / ⇧ waiting, the next letter is a shortcut (⌘J, ^C…): send it at once with the
        // modifier and keep it out of the line.
        addTextChangedListener(object : android.text.TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {
                if (mods == 0 || s == null || count <= 0 || before != 0) return
                val added = s.subSequence(start, start + count).toString()
                if (added.codePointCount(0, added.length) != 1 || added[0].code < 0x20) return
                post {
                    val e = editableText
                    if (start + count <= e.length && e.subSequence(start, start + count).toString() == added) e.delete(start, start + count)
                }
                send(KeyBytes.text(added, takeMods()))
            }
            override fun afterTextChanged(s: android.text.Editable?) {}
        })
        input = this
    }

    private fun keyBackground(active: Boolean, dp: Float) = android.graphics.drawable.GradientDrawable().apply {
        setColor(if (active) GREEN else Color.BLACK); setStroke((1.2f * dp).toInt(), DARK_GREEN); cornerRadius = 5 * dp
    }

    private fun press(key: ScreenKey) {
        when (key) {
            is ScreenKey.Modifier -> { mods = mods xor key.bit; markModifiers() }
            is ScreenKey.Text -> sendKeyBytes(KeyBytes.text(key.text, takeMods()))
            is ScreenKey.Code -> KeyBytes.code(key.keyCode, takeMods(), view.emulator?.isCursorKeysApplicationMode == true)?.let { send(it) }
            is ScreenKey.Bytes -> { takeMods(); send(key.bytes) }
            is ScreenKey.Action -> key.run()
        }
    }

    private fun takeMods(): Int { val taken = mods; if (taken != 0) { mods = 0; markModifiers() }; return taken }

    private fun markModifiers() {
        val dp = resources.displayMetrics.density
        for ((bit, v) in modifierViews) { val on = mods and bit != 0; v.background = keyBackground(on, dp); v.setTextColor(if (on) Color.BLACK else GREEN) }
    }

    /** Bytes typed on the soft keyboard: one printable character takes the waiting modifiers. */
    /** "Saved on the PC" as a phone notification, so the status line does not keep saying it. */
    private fun notifySaved(path: String) {
        val manager = getSystemService(android.app.NotificationManager::class.java)
        manager.createNotificationChannel(android.app.NotificationChannel("files", "PC に送ったファイル", android.app.NotificationManager.IMPORTANCE_DEFAULT))
        manager.notify(path.hashCode(), android.app.Notification.Builder(this, "files").setSmallIcon(kazuph.ikesu.R.drawable.ic_notification)
            .setContentTitle("${target()?.name ?: "PC"} に保存しました").setContentText(path)
            .setStyle(android.app.Notification.BigTextStyle().bigText(path)).setColor(GREEN)
            .setVisibility(android.app.Notification.VISIBILITY_PRIVATE).setAutoCancel(true).build())
    }

    private fun sendKeyBytes(bytes: ByteArray) {
        val text = String(bytes, Charsets.UTF_8)
        if (mods != 0 && text.codePointCount(0, text.length) == 1 && text[0].code >= 0x20) send(KeyBytes.text(text, takeMods())) else send(bytes)
    }

    /** Closing from here is not the person cancelling: drop the dismiss listener first, because Android runs it later. */
    private fun closeNative() { native?.setOnDismissListener(null); native?.dismiss(); native = null; nativeShown = null }

    /** Moves ikesu's selection one row toward the row being chosen, or chooses it. False when the signal is not for it. */
    private fun advanceChoice(signal: JSONObject): Boolean {
        val choice = choosing ?: return false
        if (signal.optString("kind") != "menu" || signal.optString("title") != choice.title) return false
        val raw = signal.optJSONArray("items") ?: return false
        val items = (0 until raw.length()).map { raw.optJSONObject(it) ?: JSONObject() }
        val target = items.indexOfFirst { !it.optBoolean("separator") && it.optString("label") == choice.label && it.optString("detail") == choice.detail }
        val keys = MenuKeys.step(items.map { it.optBoolean("separator") to it.optBoolean("disabled") }, signal.optInt("sel", -1), target) ?: return false
        if (keys.size == 1 && keys[0].toInt() == 13) { choosing = null; view.removeCallbacks(choiceTimeout) }
        else { view.removeCallbacks(choiceTimeout); view.postDelayed(choiceTimeout, 3000) }
        send(keys)
        return true
    }

    /** One open ikesu menu or dialog at a time. Choosing sends the same keys a keyboard would (↑/↓ Enter, Esc). */
    private fun showNative(signal: JSONObject) {
        val text = signal.toString()
        if (text == nativeShown) return
        val kind = signal.optString("kind")
        if (kind == "menu") lastMenu = signal
        if (advanceChoice(signal)) return
        if (choosing != null) { choosing = null; view.removeCallbacks(choiceTimeout) }
        answeredInput?.let { (title, typed) ->
            if (kind == "dialog" && signal.optString("title") == title && signal.optString("input") != typed) return
            answeredInput = null
        }
        closeNative()
        val dp = resources.displayMetrics.density
        when (signal.optString("kind")) {
            "menu" -> {
                val raw = signal.optJSONArray("items") ?: org.json.JSONArray()
                val items = (0 until raw.length()).map { raw.optJSONObject(it) ?: JSONObject() }
                val flags = items.map { it.optBoolean("separator") to it.optBoolean("disabled") }
                val sel = signal.optInt("sel", -1)
                val list = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
                val rows = mutableListOf<Pair<Int, View>>()
                items.forEachIndexed { index, item ->
                    if (item.optBoolean("separator")) {
                        list.addView(View(this).apply { setBackgroundColor(DARK_GREEN) }, LinearLayout.LayoutParams(-1, (1 * dp).toInt()).apply { setMargins(0, (6 * dp).toInt(), 0, (6 * dp).toInt()) })
                        return@forEachIndexed
                    }
                    val disabled = item.optBoolean("disabled")
                    val row = TextView(this).apply {
                        val extra = listOf(item.optString("detail"), item.optString("status")).filter { it.isNotEmpty() }.joinToString("  ")
                        this.text = if (extra.isEmpty()) item.optString("label") else "${item.optString("label")}\n$extra"
                        textSize = 20f; setTextColor(if (disabled) DARK_GREEN else GREEN)
                        minHeight = (56 * dp).toInt(); gravity = Gravity.CENTER_VERTICAL
                        setPadding((16 * dp).toInt(), (8 * dp).toInt(), (16 * dp).toInt(), (8 * dp).toInt())
                        background = keyBackground(index == sel, dp).also { if (index == sel) setTextColor(Color.BLACK) }
                        if (!disabled) setOnClickListener {
                            choosing = Choice(signal.optString("title"), item.optString("label"), item.optString("detail"))
                            closeNative()
                            if (!advanceChoice(signal)) choosing = null
                        }
                    }
                    list.addView(row, LinearLayout.LayoutParams(-1, -2).apply { setMargins(0, (3 * dp).toInt(), 0, (3 * dp).toInt()) })
                    rows += index to row
                }
                val body = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding((16 * dp).toInt(), (8 * dp).toInt(), (16 * dp).toInt(), 0) }
                if (signal.optBoolean("filterable")) {
                    // Narrow the list here; choosing still moves ikesu's own selection, so its filter stays untouched.
                    body.addView(android.widget.EditText(this).apply {
                        hint = "絞り込み"; setTextColor(GREEN); setHintTextColor(DARK_GREEN); textSize = 18f; isSingleLine = true
                        addTextChangedListener(object : android.text.TextWatcher {
                            override fun beforeTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {}
                            override fun onTextChanged(s: CharSequence?, a: Int, b: Int, c: Int) {
                                val q = s?.toString()?.lowercase().orEmpty()
                                rows.forEach { (i, v) -> v.visibility = if (q.isEmpty() || items[i].optString("label").lowercase().contains(q) || items[i].optString("detail").lowercase().contains(q)) View.VISIBLE else View.GONE }
                            }
                            override fun afterTextChanged(s: android.text.Editable?) {}
                        })
                    })
                }
                body.addView(android.widget.ScrollView(this).apply { addView(list) })
                native = AlertDialog.Builder(this).setTitle(signal.optString("title").ifEmpty { "メニュー" }).setView(body)
                    .setNegativeButton("閉じる", null)
                    .setOnDismissListener { nativeShown = null; send(byteArrayOf(27)) }.show().apply { setCanceledOnTouchOutside(true) }
            }
            "dialog" -> {
                val lines = signal.optJSONArray("lines")
                val message = (listOf(signal.optString("detail")) + (0 until (lines?.length() ?: 0)).map { lines!!.optString(it) }).filter { it.isNotEmpty() }.joinToString("\n")
                val field = if (signal.isNull("input") || !signal.has("input")) null else android.widget.EditText(this).apply {
                    setText(signal.optString("input")); setSelection(text.length); textSize = 18f; setTextColor(GREEN); isSingleLine = true
                }
                var answered = false
                val builder = AlertDialog.Builder(this).setTitle(signal.optString("title")).setMessage(message.ifEmpty { null })
                    .setPositiveButton(signal.optString("confirm").ifEmpty { "OK" }) { _, _ ->
                        answered = true
                        // An input dialog: clear ikesu's field (Ctrl+U), type the edited text, then Enter.
                        if (field != null) {
                            val typed = field.text.toString().replace(Regex("[\\r\\n]"), " ")
                            answeredInput = signal.optString("title") to typed
                            send(byteArrayOf(21) + typed.toByteArray(Charsets.UTF_8) + byteArrayOf(13))
                        } else send(byteArrayOf(13))
                    }
                if (!signal.optBoolean("continue_only")) builder.setNegativeButton(signal.optString("cancel").ifEmpty { "やめる" }) { _, _ -> answered = true; send(byteArrayOf(27)) }
                field?.let { builder.setView(it) }
                native = builder.setOnDismissListener { nativeShown = null; if (!answered) send(byteArrayOf(27)) }.show().apply { setCanceledOnTouchOutside(!signal.optBoolean("continue_only")) }
            }
            else -> return
        }
        nativeShown = text
    }

    private fun longPressMenu(col: Int, row: Int) {
        val items = listOf<Pair<String, () -> Unit>>(
            "ダブルクリック" to { send(KeyBytes.click(col, row) + KeyBytes.click(col, row)) },
            "クリック（1回）" to { send(KeyBytes.click(col, row)) },
            "右クリック" to { send(KeyBytes.click(col, row, 2)) },
            "貼り付け" to { pasteClipboard() },
            "写真を送る（カメラロール）" to { pickPhoto() },
            "ファイルを送る（ファイラー）" to { pickDocument() },
            "キーボードを出す" to { input?.let { it.requestFocus(); (getSystemService(INPUT_METHOD_SERVICE) as InputMethodManager).showSoftInput(it, 0) } },
        )
        AlertDialog.Builder(this).setItems(items.map { it.first }.toTypedArray()) { _, which -> items[which].second() }.show()
    }

    private fun pasteClipboard() {
        val text = (getSystemService(CLIPBOARD_SERVICE) as ClipboardManager).primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this)?.toString()
        if (text.isNullOrEmpty()) { Toast.makeText(this, "クリップボードが空です", Toast.LENGTH_SHORT).show(); return }
        val bytes = KeyBytes.paste(text)
        var start = 0
        while (start < bytes.size) { val end = minOf(bytes.size, start + INPUT_PIECE); send(bytes.copyOfRange(start, end)); start = end }
    }

    // ---- 画像・ファイル: PC に保存され、そのパスが画面に打たれる（PROTOCOL 4） ----
    /** 📎: choose between the camera roll and the file picker each time. */
    private fun pickFile() {
        AlertDialog.Builder(this).setItems(arrayOf("写真（カメラロール）", "ファイル（ファイラー）")) { _, which -> if (which == 0) pickPhoto() else pickDocument() }.show()
    }

    /** Straight to the photos: the system photo picker (no storage permission needed). */
    private fun pickPhoto() {
        val picker = Intent(android.provider.MediaStore.ACTION_PICK_IMAGES).setType("*/*")
            .putExtra(Intent.EXTRA_MIME_TYPES, arrayOf("image/*", "video/*"))
        // Android 12 without the photo picker: the regular image chooser.
        val fallback = Intent(Intent.ACTION_GET_CONTENT).addCategory(Intent.CATEGORY_OPENABLE).setType("image/*")
        picking = true
        startActivityForResult(if (picker.resolveActivity(packageManager) != null) picker else fallback, PICK_FILE)
    }

    private fun pickDocument() {
        picking = true
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), PICK_FILE)
    }

    @Deprecated("Activity result API is not used in this app")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == PICK_FILE) picking = false
        if (requestCode != PICK_FILE || resultCode != RESULT_OK) return
        val uri = data?.data ?: return
        sendFile(uri)
    }

    private fun sendFile(uri: Uri) {
        val name = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { if (it.moveToFirst()) it.getString(0) else null } ?: "file"
        status.text = "「$name」を送っています…"
        Thread {
            val result = runCatching {
                contentResolver.openInputStream(uri)!!.use { input ->
                    val bytes = input.readNBytes(MAX_FILE_BYTES + 1)
                    require(bytes.size <= MAX_FILE_BYTES) { "20 MB までのファイルを選んでください" }
                    // Encode here, off the screen's thread.
                    Base64.encodeToString(bytes, Base64.NO_WRAP)
                }
            }
            runOnUiThread {
                result.onSuccess { data ->
                    // Choosing the file sent the app to the background, which closed the screen; it is reopening now.
                    if (screenId < 0) { pendingFile = name to data; status.text = "画面が開いたら「$name」を送ります…" }
                    else sendFileNow(name, data)
                }.onFailure { error -> status.text = error.message ?: "ファイルを読めませんでした" }
            }
        }.start()
    }

    private fun sendFileNow(name: String, data: String) {
        val target = target() ?: return
        target.send(JSONObject().put("type", "screen_file").put("screen", screenId).put("beat", target.beat).put("name", name).put("data", data))
        status.text = "「$name」を送っています…"
    }

    // ---- 音声: 話した内容を文字にして、画面に打つ（Enter は押さない） ----
    private fun toggleVoice() {
        if (transcribing) return
        if (voice.recording) {
            val audio = voice.stop() ?: run { Toast.makeText(this, "録音できませんでした", Toast.LENGTH_LONG).show(); markMic(); return }
            transcribing = true; markMic(); status.text = "文字にしています…"
            val settings = SpeechSettings(this)
            Thread {
                val result = runCatching { Transcriber.transcribe(settings, audio) }
                audio.delete()
                runOnUiThread {
                    transcribing = false; markMic()
                    result.onSuccess { text ->
                        if (text.isEmpty()) { voiceDown.textDone(false); status.text = "声を聞き取れませんでした" }
                        // Line breaks would press Enter and control characters would act as keys: type plain text only.
                        else {
                            send(text.replace(Regex("[\\r\\n\\t]+"), " ").replace(Regex("\\p{Cntrl}"), "").toByteArray(Charsets.UTF_8))
                            // 文字にしている間に音量の下を押していたら、打ち終えてから Enter を1回押す。
                            // 文字と同時に届くと貼り付けとして読まれることがあるので、少し間をあけて押す。
                            if (voiceDown.textDone(true)) status.postDelayed({ send(byteArrayOf(13)) }, 200)
                            status.text = "${target()?.name ?: "PC"} の画面"
                        }
                    }.onFailure { error -> voiceDown.textDone(false); status.text = error.message ?: "文字起こしに失敗しました" }
                }
            }.start()
            return
        }
        if (SpeechSettings.endpoint(SpeechSettings(this).url).isFailure) { Toast.makeText(this, "接続設定で、音声の文字起こしの接続先を入れてください", Toast.LENGTH_LONG).show(); return }
        if (checkSelfPermission(android.Manifest.permission.RECORD_AUDIO) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(android.Manifest.permission.RECORD_AUDIO), 3); return
        }
        try { voice.start(); status.text = "録音中…" } catch (error: Exception) { Toast.makeText(this, "録音を始められませんでした: ${error.message}", Toast.LENGTH_LONG).show() }
        markMic()
    }

    private var micPulse: android.animation.ObjectAnimator? = null
    /** The 🎤 key itself shows the microphone: red and blinking while recording, yellow while turning it into text. */
    private fun markMic() {
        val mic = micView ?: return
        val dp = resources.displayMetrics.density
        micPulse?.cancel(); micPulse = null; mic.alpha = 1f
        when {
            voice.recording -> {
                mic.background = android.graphics.drawable.GradientDrawable().apply { setColor(RED); cornerRadius = 6 * dp }
                micPulse = android.animation.ObjectAnimator.ofFloat(mic, "alpha", 1f, 0.35f).apply {
                    duration = 600; repeatMode = android.animation.ValueAnimator.REVERSE; repeatCount = android.animation.ValueAnimator.INFINITE; start()
                }
            }
            transcribing -> mic.background = android.graphics.drawable.GradientDrawable().apply { setColor(0xFFFFCC33.toInt()); cornerRadius = 6 * dp }
            else -> mic.background = keyBackground(false, dp)
        }
    }

    override fun onStart() {
        super.onStart()
        registerReceiver(changes, android.content.IntentFilter(DecisionService.CHANGED), RECEIVER_NOT_EXPORTED)
        markBell()
        ScreenBus.listener = { from, message -> if (from == source) handle(message) }
        // Coming back after the unlock ended: do not keep showing the old screen.
        started = true
        if (target()?.operable != true) { dropScreen(); view.clear() }
        refresh()
    }

    override fun onDestroy() {
        running = false
        super.onDestroy()
    }

    // Leaving the screen closes it on the PC (PROTOCOL 4). Coming back opens a new one.
    override fun onStop() {
        // Choosing a photo or file is part of using this screen: closing it would start a new ikesu client on return,
        // which shows its first space instead of the one being used.
        runCatching { unregisterReceiver(changes) }
        if (picking) { super.onStop(); return }
        started = false
        dropScreen()
        if (voice.recording) { voice.cancel(); markMic() }
        ScreenBus.listener = null
        super.onStop()
    }

    private fun open(cols: Int, rows: Int) {
        val target = target()
        if (target == null || !target.operable) { status.text = "顔・指紋で開いてから、画面を開いてください"; return }
        if (opening) return
        opening = target.send(JSONObject().put("type", "screen_open").put("cols", cols).put("rows", rows))
    }

    private fun sizeChanged(cols: Int, rows: Int) {
        if (screenId >= 0) target()?.send(JSONObject().put("type", "screen_resize").put("screen", screenId).put("cols", cols).put("rows", rows))
        else if (opening) wanted = cols to rows
        else if (ScreenBus.listener != null) open(cols, rows)
    }

    private fun handle(message: JSONObject) {
        when (message.optString("type")) {
            "screen_opened" -> {
                if (!opening) return
                opening = false; screenId = message.optInt("screen")
                scanner = NativeUiScanner(); lastMenu = null
                status.text = "${target()?.name ?: "PC"} の画面"
                // The size changed while opening (the keyboard, for example): send the latest one.
                wanted?.let { (c, r) -> if (c != message.optInt("cols") || r != message.optInt("rows")) sizeChanged(c, r) }
                wanted = null
                pendingFile?.let { (name, data) -> pendingFile = null; sendFileNow(name, data) }
            }
            "screen_data" -> if (message.optInt("screen") == screenId) {
                val bytes = Base64.decode(message.optString("data"), Base64.NO_WRAP)
                val result = scanner.scan(bytes)
                result.signals.forEach { showNative(it) }
                view.feed(result.screen)
            }
            "screen_closed" -> if (message.optInt("screen") == screenId) { screenId = -1; view.clear(); Toast.makeText(this, "PC 側で画面が閉じました", Toast.LENGTH_LONG).show(); refresh() }
            "screen_file_saved" -> { status.text = "${target()?.name ?: "PC"} の画面"; notifySaved(message.optString("path")) }
            "operate_expired" -> { if (isFinishing) return; screenId = -1; dropScreen(); view.clear(); Toast.makeText(this, "接続が切れたか、顔・指紋で開いた時間が切れました", Toast.LENGTH_LONG).show(); refresh() }
            "error" -> { opening = false; status.text = "PC 側: ${message.optString("message")}" }
        }
    }

    private fun send(bytes: ByteArray) {
        if (screenId < 0 || bytes.isEmpty()) return
        val target = target() ?: return
        target.send(JSONObject().put("type", "screen_input").put("screen", screenId).put("beat", target.beat)
            .put("data", Base64.encodeToString(bytes, Base64.NO_WRAP)))
    }
}

/** Draws a Termux TerminalEmulator fed with bytes from the PC, and turns touches and the soft keyboard into input. */
class TerminalCanvas(
    context: Context,
    textSp: Float,
    private val keys: (ByteArray) -> Unit,
    private val onTap: (col: Int, row: Int) -> Unit,
    private val onLongPress: (col: Int, row: Int) -> Unit,
    private val onWheel: (col: Int, row: Int, up: Boolean, notches: Int) -> Unit,
    private val onTextSize: (Float) -> Unit,
) : View(context) {
    private var sp = textSp.coerceIn(MIN_SP, MAX_SP)
    private var renderer = makeRenderer()
    var emulator: TerminalEmulator? = null; private set
    var cols = 0; private set
    var rows = 0; private set
    var onSize: ((Int, Int) -> Unit)? = null

    companion object { const val MIN_SP = 7f; const val MAX_SP = 28f }

    private fun makeRenderer() = TerminalRenderer((sp * resources.displayMetrics.scaledDensity).toInt(), Typeface.MONOSPACE)

    /** The terminal's own answers to the host (cursor reports, colors, clipboard) stay on the phone. */
    private val silent = object : TerminalOutput() {
        override fun write(data: ByteArray, offset: Int, count: Int) {}
        override fun titleChanged(oldTitle: String?, newTitle: String?) {}
        override fun onCopyTextToClipboard(text: String?) {}
        override fun onPasteTextFromClipboard() {}
        override fun onBell() {}
        override fun onColorsChanged() {}
    }
    private val client = object : TerminalSessionClient {
        override fun onTextChanged(changedSession: TerminalSession) {}
        override fun onTitleChanged(changedSession: TerminalSession) {}
        override fun onSessionFinished(finishedSession: TerminalSession) {}
        override fun onCopyTextToClipboard(session: TerminalSession, text: String?) {}
        override fun onPasteTextFromClipboard(session: TerminalSession?) {}
        override fun onBell(session: TerminalSession) {}
        override fun onColorsChanged(session: TerminalSession) {}
        override fun onTerminalCursorStateChange(state: Boolean) {}
        override fun getTerminalCursorStyle(): Int? = null
        override fun logError(tag: String?, message: String?) {}
        override fun logWarn(tag: String?, message: String?) {}
        override fun logInfo(tag: String?, message: String?) {}
        override fun logDebug(tag: String?, message: String?) {}
        override fun logVerbose(tag: String?, message: String?) {}
        override fun logStackTraceWithMessage(tag: String?, message: String?, e: Exception?) {}
        override fun logStackTrace(tag: String?, e: Exception?) {}
    }

    // One tap is one click, as with a mouse (a tap on the space already shown opens its menu, so a tap must not
    // be two clicks). Two quick taps are a double-click: a second tap rarely lands on the same cell on a phone,
    // so its click goes to the first tap's cell.
    private var tapped: Pair<Int, Int>? = null
    private val gestures = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
        override fun onDown(e: MotionEvent) = true
        override fun onSingleTapUp(e: MotionEvent): Boolean { tapped = cell(e); tapped?.let { (c, r) -> onTap(c, r) }; return true }
        override fun onDoubleTapEvent(e: MotionEvent): Boolean {
            if (e.actionMasked == MotionEvent.ACTION_UP) (tapped ?: cell(e))?.let { (c, r) -> onTap(c, r) }
            return true
        }
        override fun onLongPress(e: MotionEvent) { cell(e)?.let { (c, r) -> onLongPress(c, r) } }
        // Dragging up and down scrolls, as in Termux: the finger moving down shows older lines. ikesu scrolls
        // MouseScrollLines (3) lines per wheel notch, so one notch per three rows keeps the text under the finger.
        override fun onScroll(e1: MotionEvent?, e2: MotionEvent, dx: Float, dy: Float): Boolean {
            flinger.forceFinished(true)
            wheelAt = cell(e1 ?: e2)
            drag(-dy)
            return true
        }
        override fun onFling(e1: MotionEvent?, e2: MotionEvent, vx: Float, vy: Float): Boolean {
            wheelAt = cell(e1 ?: e2)
            flinger.fling(0, 0, 0, vy.toInt(), 0, 0, -1_000_000, 1_000_000)
            flungY = 0
            postOnAnimation(flingStep)
            return true
        }
    })
    private var wheelAt: Pair<Int, Int>? = null
    private var dragged = 0f
    private val flinger = android.widget.OverScroller(context)
    private var flungY = 0
    private val flingStep = object : Runnable {
        override fun run() {
            if (!flinger.computeScrollOffset()) return
            drag((flinger.currY - flungY).toFloat()); flungY = flinger.currY
            postOnAnimation(this)
        }
    }

    /** [dy] pixels of finger movement (positive = down) turned into whole wheel notches. */
    private fun drag(dy: Float) {
        val (c, r) = wheelAt ?: return
        val notch = renderer.fontLineSpacing * 3f
        dragged += dy
        val notches = (dragged / notch).toInt()
        if (notches == 0) return
        dragged -= notches * notch
        // One message per touch event, however many notches it moved.
        onWheel(c, r, notches > 0, kotlin.math.abs(notches))
    }
    // Pinch in and out changes the text size, as in Termux; the column and row count follow.
    // While pinching only the drawing is scaled (smooth); the new size and the PC's columns change once, on release.
    private var pinch = 1f
    private var pinchX = 0f
    private var pinchY = 0f
    private val scaler = ScaleGestureDetector(context, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
        override fun onScaleBegin(detector: ScaleGestureDetector): Boolean { pinch = 1f; pinchX = detector.focusX; pinchY = detector.focusY; return true }
        override fun onScale(detector: ScaleGestureDetector): Boolean {
            pinch = (pinch * detector.scaleFactor).coerceIn(MIN_SP / sp, MAX_SP / sp)
            invalidate()
            return true
        }
        override fun onScaleEnd(detector: ScaleGestureDetector) {
            val next = (sp * pinch).coerceIn(MIN_SP, MAX_SP)
            pinch = 1f
            if (kotlin.math.abs(next - sp) >= 0.25f) { sp = next; renderer = makeRenderer(); remeasure(); onTextSize(sp) } else invalidate()
        }
    })

    init { isFocusable = true; isFocusableInTouchMode = true }

    override fun onTouchEvent(event: MotionEvent): Boolean {
        if (event.actionMasked == MotionEvent.ACTION_DOWN) { flinger.forceFinished(true); dragged = 0f }
        scaler.onTouchEvent(event)
        if (!scaler.isInProgress) gestures.onTouchEvent(event)
        return true
    }

    private fun cell(e: MotionEvent): Pair<Int, Int>? {
        if (cols == 0) return null
        val col = (e.x / renderer.fontWidth).toInt().coerceIn(0, cols - 1) + 1
        val row = (e.y / renderer.fontLineSpacing).toInt().coerceIn(0, rows - 1) + 1
        return col to row
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) { super.onSizeChanged(w, h, oldw, oldh); remeasure() }

    private fun remeasure() {
        if (width == 0 || height == 0) return
        val newCols = (width / renderer.fontWidth).toInt().coerceIn(20, 300)
        val newRows = (height / renderer.fontLineSpacing).coerceIn(10, 200)
        if (newCols == cols && newRows == rows) { invalidate(); return }
        cols = newCols; rows = newRows
        val cellW = renderer.fontWidth.toInt(); val cellH = renderer.fontLineSpacing
        emulator?.resize(cols, rows, cellW, cellH) ?: run { emulator = TerminalEmulator(silent, cols, rows, cellW, cellH, 2000, client) }
        onSize?.invoke(cols, rows)
        invalidate()
    }

    fun feed(bytes: ByteArray) { emulator?.append(bytes, bytes.size); invalidate() }

    /** Forgets what was drawn (so nothing of the PC stays visible), keeping a blank terminal of the same size so the
     *  next screen opens at once. */
    fun clear() { emulator = null; cols = 0; rows = 0; remeasure() }

    override fun onDraw(canvas: Canvas) {
        canvas.drawColor(Color.BLACK)
        if (pinch != 1f) { canvas.save(); canvas.scale(pinch, pinch, pinchX, pinchY) }
        emulator?.let { renderer.render(it, canvas, 0, -1, -1, -1, -1) }
        if (pinch != 1f) canvas.restore()
    }

    fun showKeyboard() {
        requestFocus()
        (context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager).showSoftInput(this, InputMethodManager.SHOW_IMPLICIT)
    }

    override fun onCheckIsTextEditor(): Boolean = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        // Plain keys without suggestions: every letter goes to the terminal as soon as it is typed.
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS or InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_FLAG_NO_EXTRACT_UI
        return object : BaseInputConnection(this, false) {
            override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
                if (text.isNotEmpty()) keys(text.toString().replace('\n', '\r').toByteArray(Charsets.UTF_8))
                return true
            }
            override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
                repeat(beforeLength.coerceAtLeast(1)) { keys(byteArrayOf(0x7f)) }
                return true
            }
            override fun sendKeyEvent(event: KeyEvent): Boolean {
                if (event.action == KeyEvent.ACTION_DOWN) onKeyDown(event.keyCode, event)
                return true
            }
        }
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        when (keyCode) {
            KeyEvent.KEYCODE_ENTER -> keys(byteArrayOf(13))
            KeyEvent.KEYCODE_DEL -> keys(byteArrayOf(0x7f))
            KeyEvent.KEYCODE_BACK -> return super.onKeyDown(keyCode, event)
            else -> {
                val code = KeyHandler.getCode(keyCode, 0, emulator?.isCursorKeysApplicationMode == true, false)
                if (code != null) keys(code.toByteArray())
                else event.unicodeChar.takeIf { it != 0 }?.let { keys(String(Character.toChars(it)).toByteArray(Charsets.UTF_8)) }
                    ?: return super.onKeyDown(keyCode, event)
            }
        }
        return true
    }
}
