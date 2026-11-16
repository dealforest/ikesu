package kazuph.ikesu.decisions

import android.app.Activity
import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Typeface
import android.os.Bundle
import android.text.InputType
import android.util.Base64
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.view.WindowManager
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import android.widget.Button
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

/** The whole ikesu screen of one PC, drawn at the phone's size (PROTOCOL 4). Keys go to the PC; the terminal's own
 *  automatic replies (queries, clipboard) are never sent. */
class ScreenActivity : Activity() {
    companion object {
        const val SOURCE = "source"
        private const val GREEN = 0xFF33FF66.toInt()
        private const val DARK_GREEN = 0xFF1E9948.toInt()
    }

    private lateinit var source: String
    private lateinit var view: TerminalCanvas
    private lateinit var status: TextView
    private var screenId = -1
    /** A screen_open is on its way; size changes wait for screen_opened instead of opening a second screen. */
    private var opening = false
    private var wanted: Pair<Int, Int>? = null
    private var ctrl = false
    private var ctrlButton: Button? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
        source = intent.getStringExtra(SOURCE) ?: run { finish(); return }
        val name = DecisionState.sources[source]?.name ?: "PC"
        view = TerminalCanvas(this) { bytes -> send(bytes) }
        val dp = resources.displayMetrics.density
        status = TextView(this).apply { text = "$name の画面を開いています…"; setTextColor(GREEN); textSize = 18f; setPadding((20 * dp).toInt(), (6 * dp).toInt(), (20 * dp).toInt(), (6 * dp).toInt()) }
        val keys = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; setBackgroundColor(Color.BLACK); setPadding((4 * dp).toInt(), (4 * dp).toInt(), (4 * dp).toInt(), (4 * dp).toInt()) }
        fun key(label: String, action: () -> Unit) = Button(this).apply {
            text = label; isAllCaps = false; textSize = 18f; minWidth = 0; minimumWidth = 0; minHeight = (52 * dp).toInt(); setPadding(0, 0, 0, 0)
            setTextColor(GREEN)
            background = android.graphics.drawable.GradientDrawable().apply { setColor(Color.BLACK); setStroke((1.5f * dp).toInt(), DARK_GREEN); cornerRadius = 6 * dp }
            setOnClickListener { action() }
        }.also { keys.addView(it, LinearLayout.LayoutParams(0, -2, 1f).apply { marginStart = (3 * dp).toInt(); marginEnd = (3 * dp).toInt() }) }
        key("Esc") { send(byteArrayOf(27)) }
        key("Tab") { send(byteArrayOf(9)) }
        ctrlButton = key("Ctrl") { ctrl = !ctrl; markCtrl() }
        for ((label, code) in listOf("←" to KeyEvent.KEYCODE_DPAD_LEFT, "↓" to KeyEvent.KEYCODE_DPAD_DOWN, "↑" to KeyEvent.KEYCODE_DPAD_UP, "→" to KeyEvent.KEYCODE_DPAD_RIGHT))
            key(label) { sendCode(code) }
        key("⌨") { view.showKeyboard() }
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK)
            addView(status, LinearLayout.LayoutParams(-1, -2))
            addView(view, LinearLayout.LayoutParams(-1, 0, 1f))
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
        view.onSize = { cols, rows -> sizeChanged(cols, rows) }
    }

    override fun onStart() {
        super.onStart()
        ScreenBus.listener = { from, message -> if (from == source) handle(message) }
        if (view.cols > 0) open(view.cols, view.rows)
    }

    // Leaving the screen closes it on the PC (PROTOCOL 4). Coming back opens a new one.
    override fun onStop() {
        if (screenId >= 0) DecisionState.sources[source]?.send(JSONObject().put("type", "screen_close").put("screen", screenId))
        screenId = -1; opening = false; wanted = null
        ScreenBus.listener = null
        super.onStop()
    }

    private fun open(cols: Int, rows: Int) {
        val target = DecisionState.sources[source]
        if (target == null || !target.operable) { status.text = "顔・指紋で開いてから、画面を開いてください"; return }
        if (opening) return
        opening = target.send(JSONObject().put("type", "screen_open").put("cols", cols).put("rows", rows))
    }

    private fun sizeChanged(cols: Int, rows: Int) {
        if (screenId >= 0) DecisionState.sources[source]?.send(JSONObject().put("type", "screen_resize").put("screen", screenId).put("cols", cols).put("rows", rows))
        else if (opening) wanted = cols to rows
        else if (ScreenBus.listener != null) open(cols, rows)
    }

    private fun handle(message: JSONObject) {
        when (message.optString("type")) {
            "screen_opened" -> {
                if (!opening) return
                opening = false; screenId = message.optInt("screen")
                status.text = "${DecisionState.sources[source]?.name ?: "PC"} の画面"
                // The size changed while opening (the keyboard, for example): send the latest one.
                wanted?.let { (c, r) -> if (c != message.optInt("cols") || r != message.optInt("rows")) sizeChanged(c, r) }
                wanted = null
            }
            "screen_data" -> if (message.optInt("screen") == screenId) view.feed(Base64.decode(message.optString("data"), Base64.NO_WRAP))
            "screen_closed" -> if (message.optInt("screen") == screenId) { screenId = -1; status.text = "画面が閉じました"; finish() }
            "operate_expired" -> { screenId = -1; Toast.makeText(this, "顔・指紋で開いた時間が切れました", Toast.LENGTH_LONG).show(); finish() }
            "error" -> { opening = false; status.text = "PC 側: ${message.optString("message")}" }
        }
    }

    /** Ctrl waiting for the next letter is filled green, like a selected button. */
    private fun markCtrl() {
        val dp = resources.displayMetrics.density
        ctrlButton?.setTextColor(if (ctrl) Color.BLACK else GREEN)
        ctrlButton?.background = android.graphics.drawable.GradientDrawable().apply { setColor(if (ctrl) GREEN else Color.BLACK); setStroke((1.5f * dp).toInt(), DARK_GREEN); cornerRadius = 6 * dp }
    }

    private fun sendCode(keyCode: Int) {
        val code = KeyHandler.getCode(keyCode, 0, view.emulator?.isCursorKeysApplicationMode == true, false) ?: return
        send(code.toByteArray())
    }

    private fun send(bytes: ByteArray) {
        if (screenId < 0) return
        val data = if (ctrl && bytes.size == 1 && bytes[0] in 0x40..0x7e) { ctrl = false; markCtrl(); byteArrayOf((bytes[0].toInt() and 0x1f).toByte()) } else bytes
        val target = DecisionState.sources[source] ?: return
        target.send(JSONObject().put("type", "screen_input").put("screen", screenId).put("beat", target.beat)
            .put("data", Base64.encodeToString(data, Base64.NO_WRAP)))
    }
}

/** Draws a Termux TerminalEmulator fed with bytes from the PC, and turns the soft keyboard into bytes. */
class TerminalCanvas(context: Context, private val keys: (ByteArray) -> Unit) : View(context) {
    private val renderer = TerminalRenderer((14f * resources.displayMetrics.scaledDensity).toInt(), Typeface.MONOSPACE)
    var emulator: TerminalEmulator? = null; private set
    var cols = 0; private set
    var rows = 0; private set
    var onSize: ((Int, Int) -> Unit)? = null

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

    init { isFocusable = true; isFocusableInTouchMode = true; setOnClickListener { showKeyboard() } }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        val newCols = (w / renderer.fontWidth).toInt().coerceIn(20, 300)
        val newRows = (h / renderer.fontLineSpacing).coerceIn(10, 200)
        if (newCols == cols && newRows == rows) return
        cols = newCols; rows = newRows
        val cellW = renderer.fontWidth.toInt(); val cellH = renderer.fontLineSpacing
        emulator?.resize(cols, rows, cellW, cellH) ?: run { emulator = TerminalEmulator(silent, cols, rows, cellW, cellH, 2000, client) }
        onSize?.invoke(cols, rows)
        invalidate()
    }

    fun feed(bytes: ByteArray) { emulator?.append(bytes, bytes.size); invalidate() }

    override fun onDraw(canvas: Canvas) {
        canvas.drawColor(Color.BLACK)
        emulator?.let { renderer.render(it, canvas, 0, -1, -1, -1, -1) }
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
