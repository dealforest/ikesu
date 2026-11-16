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
    fun click(col: Int, row: Int, button: Int = 0): ByteArray = "\u001b[<$button;$col;${row}M\u001b[<$button;$col;${row}m".toByteArray()

    /** Text pasted as one block, so the program can tell it from typing. */
    fun paste(text: String): ByteArray = "\u001b[200~${text.replace("\u001b", "")}\u001b[201~".toByteArray(Charsets.UTF_8)
}

/** The whole ikesu screen of one PC, drawn at the phone's size (PROTOCOL 4). Keys go to the PC; the terminal's own
 *  automatic replies (queries, clipboard) are never sent. */
class ScreenActivity : Activity() {
    companion object {
        const val SOURCE = "source"
        private const val GREEN = 0xFF33FF66.toInt()
        private const val DARK_GREEN = 0xFF1E9948.toInt()
        private const val PICK_FILE = 41
        private const val MAX_FILE_BYTES = 20 * 1024 * 1024
        /** The PC refuses one screen_input over 64 KB; paste in pieces below that. */
        private const val INPUT_PIECE = 48 * 1024
    }

    private lateinit var source: String
    private lateinit var view: TerminalCanvas
    private lateinit var status: TextView
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
    /** A file read while the screen was (re)opening, sent once screen_opened arrives. */
    private var pendingFile: Pair<String, String>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE)
        source = intent.getStringExtra(SOURCE) ?: run { finish(); return }
        val name = DecisionState.sources[source]?.name ?: "PC"
        val dp = resources.displayMetrics.density
        val screenSp = getSharedPreferences("ui", MODE_PRIVATE).getFloat("screenTextSp", 13f)
        view = TerminalCanvas(this, screenSp, keys = { bytes -> sendKeyBytes(bytes) }, onTap = { col, row -> send(KeyBytes.click(col, row) + KeyBytes.click(col, row)) },
            onLongPress = { col, row -> longPressMenu(col, row) },
            onTextSize = { sp -> getSharedPreferences("ui", MODE_PRIVATE).edit().putFloat("screenTextSp", sp).apply() })
        status = TextView(this).apply { text = "$name の画面を開いています…"; setTextColor(GREEN); textSize = 18f; setPadding((20 * dp).toInt(), (6 * dp).toInt(), (20 * dp).toInt(), (6 * dp).toInt()) }

        // The owner's Termux layout (dotfiles/.termux/termux.properties), plus ⇧, ⌘, the ikesu prefix, voice, files and the keyboard.
        val rows = listOf(
            listOf(ExtraKey("⇧", ScreenKey.Modifier(KeyBytes.SHIFT)), ExtraKey("⌘", ScreenKey.Modifier(KeyBytes.SUPER)),
                ExtraKey("^T", ScreenKey.Bytes(byteArrayOf(20))),
                ExtraKey("🎤", ScreenKey.Action { toggleVoice() }), ExtraKey("📎", ScreenKey.Action { pickFile() }),
                ExtraKey("貼付", ScreenKey.Action { pasteClipboard() })),
            listOf(ExtraKey("ESC", ScreenKey.Code(KeyEvent.KEYCODE_ESCAPE)), ExtraKey("|", ScreenKey.Text("|"), "PGUP", ScreenKey.Code(KeyEvent.KEYCODE_PAGE_UP)),
                ExtraKey("$", ScreenKey.Text("$"), "PGDN", ScreenKey.Code(KeyEvent.KEYCODE_PAGE_DOWN)), ExtraKey("/", ScreenKey.Text("/"), "clear", ScreenKey.Text("/clear")),
                ExtraKey("#", ScreenKey.Text("#")), ExtraKey("↑", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_UP), "new", ScreenKey.Bytes(byteArrayOf(20))),
                ExtraKey("@", ScreenKey.Text("@")), ExtraKey("\\", ScreenKey.Text("\\")), ExtraKey("~", ScreenKey.Text("~"), "^", ScreenKey.Text("^"))),
            listOf(ExtraKey("TAB", ScreenKey.Code(KeyEvent.KEYCODE_TAB), "S-TAB", ScreenKey.Bytes("\u001b[Z".toByteArray())),
                ExtraKey("CTRL", ScreenKey.Modifier(KeyBytes.CTRL), "^C", ScreenKey.Bytes(byteArrayOf(3))), ExtraKey("ALT", ScreenKey.Modifier(KeyBytes.ALT)),
                ExtraKey("!", ScreenKey.Text("!")), ExtraKey("←", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_LEFT), "prev", ScreenKey.Bytes(byteArrayOf(16))),
                ExtraKey("↓", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_DOWN)), ExtraKey("→", ScreenKey.Code(KeyEvent.KEYCODE_DPAD_RIGHT), "next", ScreenKey.Bytes(byteArrayOf(14))),
                ExtraKey("⏎", ScreenKey.Code(KeyEvent.KEYCODE_ENTER), "\\⏎", ScreenKey.Bytes("\\\r".toByteArray())), ExtraKey("BKSP", ScreenKey.Code(KeyEvent.KEYCODE_DEL))),
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
            addView(status, LinearLayout.LayoutParams(-1, -2))
            addView(view, LinearLayout.LayoutParams(-1, 0, 1f))
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
    }

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
        var downY = 0f; var popupDone = false
        val longPress = Runnable { if (key.popup != null) { popupDone = true; press(key.popup) } }
        setOnTouchListener { v, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> { downY = event.y; popupDone = false; v.postDelayed(longPress, 400); v.isPressed = true }
                MotionEvent.ACTION_MOVE -> if (!popupDone && key.popup != null && downY - event.y > 30 * dp) { v.removeCallbacks(longPress); popupDone = true; press(key.popup) }
                MotionEvent.ACTION_UP -> { v.removeCallbacks(longPress); v.isPressed = false; if (!popupDone) press(key.main) }
                MotionEvent.ACTION_CANCEL -> { v.removeCallbacks(longPress); v.isPressed = false }
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
    private fun sendKeyBytes(bytes: ByteArray) {
        val text = String(bytes, Charsets.UTF_8)
        if (mods != 0 && text.codePointCount(0, text.length) == 1 && text[0].code >= 0x20) send(KeyBytes.text(text, takeMods())) else send(bytes)
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
        startActivityForResult(if (picker.resolveActivity(packageManager) != null) picker else fallback, PICK_FILE)
    }

    private fun pickDocument() {
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), PICK_FILE)
    }

    @Deprecated("Activity result API is not used in this app")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
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
                    if (screenId < 0) { pendingFile = name to data; status.text = "画面を開き直してから「$name」を送ります…" }
                    else sendFileNow(name, data)
                }.onFailure { error -> status.text = error.message ?: "ファイルを読めませんでした" }
            }
        }.start()
    }

    private fun sendFileNow(name: String, data: String) {
        val target = DecisionState.sources[source] ?: return
        target.send(JSONObject().put("type", "screen_file").put("screen", screenId).put("beat", target.beat).put("name", name).put("data", data))
        status.text = "「$name」を送りました。PC に保存しています…"
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
                        if (text.isEmpty()) status.text = "声を聞き取れませんでした"
                        // Line breaks would press Enter and control characters would act as keys: type plain text only.
                        else { send(text.replace(Regex("[\\r\\n\\t]+"), " ").replace(Regex("\\p{Cntrl}"), "").toByteArray(Charsets.UTF_8)); status.text = "${DecisionState.sources[source]?.name ?: "PC"} の画面" }
                    }.onFailure { error -> status.text = error.message ?: "文字起こしに失敗しました" }
                }
            }.start()
            return
        }
        if (SpeechSettings.endpoint(SpeechSettings(this).url).isFailure) { Toast.makeText(this, "接続設定で、音声の文字起こしの接続先を入れてください", Toast.LENGTH_LONG).show(); return }
        if (checkSelfPermission(android.Manifest.permission.RECORD_AUDIO) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(android.Manifest.permission.RECORD_AUDIO), 3); return
        }
        try { voice.start(); status.text = "録音中…（🎤 でもう一度押すと文字にして打ちます）" } catch (error: Exception) { Toast.makeText(this, "録音を始められませんでした: ${error.message}", Toast.LENGTH_LONG).show() }
        markMic()
    }

    private fun markMic() { /* The status line says what the microphone is doing. */ }

    override fun onStart() {
        super.onStart()
        ScreenBus.listener = { from, message -> if (from == source) handle(message) }
        // Coming back after the unlock ended: do not keep showing the old screen.
        if (DecisionState.sources[source]?.operable != true) { view.clear(); finish(); return }
        if (view.cols > 0) open(view.cols, view.rows)
    }

    // Leaving the screen closes it on the PC (PROTOCOL 4). Coming back opens a new one.
    override fun onStop() {
        if (screenId >= 0) DecisionState.sources[source]?.send(JSONObject().put("type", "screen_close").put("screen", screenId))
        screenId = -1; opening = false; wanted = null
        if (voice.recording) voice.cancel()
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
                pendingFile?.let { (name, data) -> pendingFile = null; sendFileNow(name, data) }
            }
            "screen_data" -> if (message.optInt("screen") == screenId) view.feed(Base64.decode(message.optString("data"), Base64.NO_WRAP))
            "screen_closed" -> if (message.optInt("screen") == screenId) { screenId = -1; Toast.makeText(this, "PC 側で画面が閉じました", Toast.LENGTH_LONG).show(); finish() }
            "screen_file_saved" -> status.text = "PC に保存しました: ${message.optString("path")}"
            "operate_expired" -> { if (isFinishing) return; screenId = -1; view.clear(); Toast.makeText(this, "接続が切れたか、顔・指紋で開いた時間が切れました", Toast.LENGTH_LONG).show(); finish() }
            "error" -> { opening = false; status.text = "PC 側: ${message.optString("message")}" }
        }
    }

    private fun send(bytes: ByteArray) {
        if (screenId < 0 || bytes.isEmpty()) return
        val target = DecisionState.sources[source] ?: return
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

    // A tap is hard to land twice on the same cell on a phone, so one tap sends a double-click (the owner's request).
    private val gestures = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
        override fun onDown(e: MotionEvent) = true
        override fun onSingleTapUp(e: MotionEvent): Boolean { cell(e)?.let { (c, r) -> onTap(c, r) }; return true }
        override fun onLongPress(e: MotionEvent) { cell(e)?.let { (c, r) -> onLongPress(c, r) } }
    })
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

    /** Forgets what was drawn (so nothing of the PC stays visible). */
    fun clear() { emulator = null; cols = 0; rows = 0; invalidate() }

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
