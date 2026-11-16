package kazuph.ikesu.decisions

import android.Manifest
import android.app.Activity
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.text.Editable
import android.text.TextWatcher
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import android.text.SpannableString
import android.text.Spanned
import android.text.style.URLSpan
import android.view.Gravity
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

class MainActivity : Activity() {
    private val green = 0xFF33FF66.toInt()
    private val dim = 0xFF1E9948.toInt()
    private val warn = 0xFFFFCC33.toInt()
    private data class Draft(var text: String = "", val selected: MutableSet<String> = mutableSetOf(),
                             val images: MutableList<ByteArray> = mutableListOf(), var cursor: CursorRef = CursorRef(),
                             var sequence: String? = null, var waitingReconnect: Boolean = false,
                             var uncertain: Boolean = false, var submittedItem: JSONObject? = null,
                             var cursorNavigated: Boolean = false)
    private val drafts = mutableMapOf<String, Draft>()
    private var selectedId: String? = null
    /** The PC whose screen was asked for before the unlock: open it as soon as that PC accepts the signature. */
    private var screenAfterUnlock: String? = null
    private var pendingCamera: Uri? = null
    private var pendingAttachmentId: String? = null
    private var settingsVisible = false
    private var showList = true
    private var listCursor = 0
    private var textScale = 1f
    private var appliedScale = 1f
    private var scaling = false
    private val scaleDetector by lazy {
        ScaleGestureDetector(this, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
            override fun onScaleBegin(detector: ScaleGestureDetector): Boolean {
                scaling = true
                val cancel = MotionEvent.obtain(0, 0, MotionEvent.ACTION_CANCEL, 0f, 0f, 0)
                super@MainActivity.dispatchTouchEvent(cancel); cancel.recycle()
                return true
            }
            override fun onScale(detector: ScaleGestureDetector): Boolean {
                textScale = TextScale.clamp(textScale * detector.scaleFactor)
                if (TextScale.needsRedraw(appliedScale, textScale)) applyScale(false)
                return true
            }
            override fun onScaleEnd(detector: ScaleGestureDetector) { applyScale(true) }
        }).apply { isQuickScaleEnabled = false; isStylusScaleEnabled = false }
    }
    private fun applyScale(save: Boolean) {
        if (save) getSharedPreferences("ui", MODE_PRIVATE).edit().putFloat("textScale", textScale).apply()
        if (appliedScale != textScale && (save || TextScale.needsRedraw(appliedScale, textScale))) { appliedScale = textScale; render() }
    }
    private var imeVisible = false
    private fun editorFocused(): Boolean = imeVisible && currentFocus is EditText
    private fun dp(value: Int): Int = (value * resources.displayMetrics.density + .5f).toInt()
    private val root by lazy { LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK) } }
    private val header by lazy { LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; gravity = Gravity.CENTER_VERTICAL } }
    private val layout by lazy { LinearLayout(this).apply { orientation = LinearLayout.VERTICAL } }
    private val footer by lazy { LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL } }
    private val nav = VolumeNav({ move(-1) }, { move(1) }, { confirm() }, { !settingsVisible }, { goBack() })
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) { handleUpdate() }
    }
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // What the PCs show (questions, screens) never appears in screenshots or the app switcher.
        window.setFlags(android.view.WindowManager.LayoutParams.FLAG_SECURE, android.view.WindowManager.LayoutParams.FLAG_SECURE)
        // Recordings left by a killed process are never sent.
        cacheDir.listFiles { file -> file.name.startsWith("voice") && file.name.endsWith(".m4a") }?.forEach { it.delete() }
        window.isNavigationBarContrastEnforced = false
        window.isStatusBarContrastEnforced = false
        textScale = TextScale.clamp(getSharedPreferences("ui", MODE_PRIVATE).getFloat("textScale", 1f)); appliedScale = textScale
        root.addView(header)
        root.addView(ScrollView(this).apply { setBackgroundColor(Color.BLACK); addView(layout) }, LinearLayout.LayoutParams(-1, 0, 1f))
        root.addView(footer)
        setContentView(root.apply {
            setOnApplyWindowInsetsListener { view, insets ->
                val bars = insets.getInsets(android.view.WindowInsets.Type.systemBars() or android.view.WindowInsets.Type.displayCutout())
                // Edge-to-edge does not resize for the keyboard, so keep the content above it.
                val ime = insets.getInsets(android.view.WindowInsets.Type.ime())
                view.setPadding(bars.left + dp(20), bars.top, bars.right + dp(20), maxOf(bars.bottom, ime.bottom))
                val nowVisible = insets.isVisible(android.view.WindowInsets.Type.ime())
                if (nowVisible && !imeVisible) (currentFocus as? EditText)?.let { field ->
                    field.post { field.requestRectangleOnScreen(android.graphics.Rect(0, 0, field.width, field.height), false) }
                }
                if (imeVisible && !nowVisible) (currentFocus as? EditText)?.clearFocus()
                imeVisible = nowVisible
                insets
            }
        })
        registerReceiver(receiver, IntentFilter(DecisionService.CHANGED), RECEIVER_NOT_EXPORTED)
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        receiveIntent(intent)
        startForegroundService(Intent(this, DecisionService::class.java))
        render()
    }
    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); setIntent(intent); receiveIntent(intent); render() }
    override fun onDestroy() { viewer?.dismiss(); videoViewer?.dismiss(); unregisterReceiver(receiver); super.onDestroy() }
    private fun receiveIntent(intent: Intent) {
        TextScale.fromIntent((applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0, intent.hasExtra("text_scale"), intent.getFloatExtra("text_scale", 1f))?.let { textScale = it; appliedScale = textScale; getSharedPreferences("ui", MODE_PRIVATE).edit().putFloat("textScale", textScale).apply() }
        intent.getStringExtra("decision")?.let { selectedId = it; showList = false; settingsVisible = false }
        if (intent.getBooleanExtra("list", false)) { showList = true; settingsVisible = false }
    }
    private fun handleUpdate() {
        screenAfterUnlock?.let { id ->
            val source = DecisionState.sources[id]
            if (source == null || !source.connected) screenAfterUnlock = null
            else if (source.operable) { screenAfterUnlock = null; startActivity(Intent(this, ScreenActivity::class.java).putExtra(ScreenActivity.SOURCE, id)) }
        }
        // A video removed with its decision closes the player and frees its preview.
        videoPreviews.keys.retainAll(DecisionState.videos.keys)
        videoViewer?.let { if (it.key !in DecisionState.videos) it.dismiss() }
        // Each draft follows the PC its decision came from: another PC staying connected does not count.
        val cut = drafts.filter { (id, draft) -> draft.sequence != null && !draft.waitingReconnect && !DecisionState.reachable(id) }
        if (cut.isNotEmpty()) {
            cut.values.forEach { it.waitingReconnect = true }
            Toast.makeText(this, "接続が切れました。再接続後に保存状態を確認してください", Toast.LENGTH_LONG).show()
        }
        DecisionState.result?.let { result ->
            val sequence = result.optString("sequence")
            val draft = drafts.values.firstOrNull { it.sequence == sequence }
            if (draft != null) {
                val status = result.optString("status")
                draft.uncertain = status == "unknown"
                if (!draft.uncertain) draft.sequence = null
                draft.waitingReconnect = false
                val message = when (status) {
                    "answered" -> "回答しました"
                    "answered_elsewhere" -> "ほかの窓口で回答されました"
                    "cancelled" -> "判断待ちは取り消されました"
                    "expired" -> "判断待ちは期限切れです"
                    else -> result.optString("message", "回答できませんでした")
                }
                Toast.makeText(this, message, Toast.LENGTH_LONG).show()
            }
            DecisionState.result = null
        }
        val ids = (0 until DecisionState.decisions.length()).mapNotNull { DecisionState.decisions.optJSONObject(it)?.optString("id") }.toSet()
        listCursor = if (ids.isEmpty()) 0 else listCursor.coerceAtMost(ids.size - 1)
        for ((id, draft) in drafts) {
            if (draft.waitingReconnect && draft.sequence != null && DecisionState.reachable(id)) {
                if (DecisionState.answer(id, draft.selected.toList(), draft.text, draft.images, draft.sequence!!)) draft.waitingReconnect = false
            }
        }
        drafts.entries.removeAll { it.key !in ids && it.value.sequence == null }
        if (selectedId !in ids && drafts[selectedId]?.sequence == null) { selectedId = null; showList = true }
        if (settingsVisible) {
            (header.getChildAt(0) as? TextView)?.text = "ikesu · ${if (DecisionState.connected) "接続中" else "未接続"}"
        } else render()
    }
    private fun current(): JSONObject? {
        val items = DecisionState.decisions
        for (i in 0 until items.length()) items.optJSONObject(i)?.let { if (it.optString("id") == selectedId) return it }
        return drafts[selectedId]?.takeIf { it.sequence != null }?.submittedItem
    }
    private fun frame(fill: Int = Color.BLACK): GradientDrawable = GradientDrawable().apply {
        setColor(fill); setStroke(dp(2), green); cornerRadius = dp(8).toFloat()
    }
    private fun text(value: String, size: Float = 22f): TextView = TextView(this).apply {
        text = linkified(value); textSize = size * textScale; setTextColor(green); setLinkTextColor(green)
        gravity = Gravity.START; if (size == 28f) setTypeface(typeface, Typeface.BOLD)
        linksClickable = true; setTextIsSelectable(true)
        movementMethod = android.text.method.LinkMovementMethod.getInstance()
    }
    /** One line of a multi-line command: number, label, joiner and text. Flagged lines get a red bar, tint and reason. */
    private fun unitRow(unit: JSONObject, number: Int): View {
        val verdict = unit.optString("verdict")
        val flagged = isFlaggedVerdict(verdict)
        val red = 0xFFFF4D4D.toInt()
        val ink = if (flagged) red else dim
        fun cell(value: String, size: Float, color: Int, bold: Boolean = false, mono: Boolean = false) = text(value, size).apply {
            setTextColor(color); setTextIsSelectable(!flagged || mono)
            if (bold) setTypeface(typeface, Typeface.BOLD) else if (size == 28f) setTypeface(typeface, Typeface.NORMAL)
            if (mono) typeface = if (bold) Typeface.create(Typeface.MONOSPACE, Typeface.BOLD) else Typeface.MONOSPACE
        }
        val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL; setPadding(0, dp(6), dp(8), dp(6)) }
        if (flagged) row.setBackgroundColor(0x33FF4D4D)
        row.addView(View(this).apply { setBackgroundColor(if (flagged) red else Color.TRANSPARENT) }, LinearLayout.LayoutParams(dp(5), -1).apply { marginEnd = dp(8) })
        row.addView(cell("$number", 20f, ink, flagged), LinearLayout.LayoutParams(dp(28), -2))
        row.addView(cell(unit.optString("label"), 18f, ink, flagged), LinearLayout.LayoutParams(dp(64), -2))
        val column = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val joiner = unitJoiner(unit.optString("op"))
        val line = (if (joiner.isEmpty()) "" else "$joiner ") + unit.optString("text")
        column.addView(cell(line, 20f, if (flagged) green else dim, mono = true).apply {
            if (!flagged) { maxLines = 2; ellipsize = android.text.TextUtils.TruncateAt.END }
        })
        if (flagged) for (note in unitNotes(unit.optString("target"), unit.optString("reason"), unit.optString("rule"))) {
            column.addView(cell(note, 18f, red))
        }
        row.addView(column, LinearLayout.LayoutParams(0, -2, 1f))
        if (!flagged) row.addView(cell(unitAllowNote(verdict), 18f, dim), LinearLayout.LayoutParams(-2, -2).apply { marginStart = dp(8) })
        return row
    }
    private fun linkified(value: String): CharSequence {
        val ranges = UrlFinder.find(value)
        if (ranges.isEmpty()) return value
        return SpannableString(value).apply { for (r in ranges) setSpan(URLSpan(value.substring(r)), r.first, r.last + 1, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE) }
    }
    private fun button(label: String, filled: Boolean = false, focused: Boolean = false, action: () -> Unit): Button = Button(this).apply {
        text = label; textSize = 22f * textScale; isAllCaps = false; gravity = Gravity.START or Gravity.CENTER_VERTICAL
        setTextColor(if (filled || focused) Color.BLACK else green)
        background = if (filled && focused) ring() else frame(if (filled || focused) green else Color.BLACK)
        if (filled) setTypeface(typeface, Typeface.BOLD)
        minHeight = dp(64); setPadding(dp(16), 0, dp(16), 0); setOnClickListener { action() }
    }
    /** Cursor mark for a button that is already filled: green fill inside a black gap and a green outline. */
    private fun ring(): android.graphics.drawable.Drawable {
        val outer = frame(); val inner = GradientDrawable().apply { setColor(green); cornerRadius = dp(4).toFloat() }
        return android.graphics.drawable.LayerDrawable(arrayOf(outer, inner)).apply { setLayerInset(1, dp(8), dp(8), dp(8), dp(8)) }
    }
    private fun block(view: View, margin: Int = 12) {
        layout.addView(view, LinearLayout.LayoutParams(-1, -2).apply { bottomMargin = dp(margin) })
    }
    private fun pair(left: View, right: View, target: LinearLayout) {
        target.addView(left, LinearLayout.LayoutParams(0, -2, 1f).apply { marginEnd = dp(6) })
        target.addView(right, LinearLayout.LayoutParams(0, -2, 1f).apply { marginStart = dp(6) })
    }
    private fun input(hintText: String, lines: Int = 1): EditText = EditText(this).apply {
        textSize = 22f * textScale; setTextColor(green); setHintTextColor(dim); hint = hintText
        background = frame(); gravity = Gravity.TOP or Gravity.START; minLines = lines
        setPadding(dp(16), dp(16), dp(16), dp(16))
    }
    private var commentView: EditText? = null
    private fun optionIds(item: JSONObject): List<String> {
        val options = item.optJSONArray("options") ?: return emptyList()
        return (0 until options.length()).map { options.optJSONObject(it)?.optString("id") ?: "" }
    }
    private fun cursorOf(item: JSONObject, draft: Draft): Int {
        val resolved = DetailCursor.resolve(draft.cursor, optionIds(item), item.optBoolean("allow_text"), draft.cursorNavigated)
        draft.cursor = resolved.ref; draft.cursorNavigated = resolved.navigated
        return resolved.index
    }
    private fun goBack() {
        when (BackNav.target(settingsVisible, showList)) {
            BackTarget.CLOSE_SETTINGS -> { settingsVisible = false; render() }
            BackTarget.TO_LIST -> { showList = true; render() }
            BackTarget.NOTHING -> {}
        }
    }
    private fun render(scrollToCursor: Boolean = false) {
        // A recording belongs to the decision on screen; leaving it stops the microphone.
        if (voice.recording && (settingsVisible || showList || current()?.optString("id") != voiceDecision)) {
            voice.cancel(); voiceDecision = null
            Toast.makeText(this, "画面を移ったので、録音を止めました", Toast.LENGTH_SHORT).show()
        }
        header.removeAllViews(); layout.removeAllViews(); footer.removeAllViews()
        header.addView(text("ikesu · ${if (DecisionState.connected) "接続中" else "未接続"}", 18f), LinearLayout.LayoutParams(0, -2, 1f))
        val headerAction = text(if (settingsVisible || !showList) "一覧へ" else "設定", 18f).apply {
            setTextIsSelectable(false); movementMethod = null
            gravity = Gravity.END; setPadding(dp(8), dp(12), 0, dp(12)); setOnClickListener {
                if (settingsVisible) settingsVisible = false else if (!showList) showList = true else settingsVisible = true
                render()
            }
        }
        DecisionState.sources.values.firstOrNull { it.connected && it.operable }?.let { source ->
            header.addView(text("🖥️", 22f).apply {
                setTextIsSelectable(false); movementMethod = null; contentDescription = "${source.name} の画面"
                setPadding(dp(14), dp(10), dp(6), dp(10))
                setOnClickListener { startActivity(Intent(this@MainActivity, ScreenActivity::class.java).putExtra(ScreenActivity.SOURCE, source.id)) }
            })
        }
        header.addView(headerAction)
        if (settingsVisible) { renderSettings(); return }
        val items = DecisionState.decisions
        if (showList) {
            block(text("判断待ち", 28f))
            block(text("${items.length()} 件", 18f))
            // Each PC's whole ikesu screen, once opened with the face or fingerprint (PROTOCOL 4).
            for (source in DecisionState.sources.values.filter { it.connected }) {
                if (source.operable) block(button("🖥️ ${source.name} の画面を開く") {
                    startActivity(Intent(this, ScreenActivity::class.java).putExtra(ScreenActivity.SOURCE, source.id))
                })
                else block(button("🖥️ ${source.name} の画面（顔・指紋で開く）") { screenAfterUnlock = source.id; unlock() })
            }
            for (i in 0 until items.length()) items.optJSONObject(i)?.let { entry ->
                val id = entry.optString("id")
                val row = optionRow(i, decisionHeading(entry.optString("raw_title"), entry.optString("raw_body"), entry.optString("kind")), entry.optString("location"), i == listCursor, false, supplement = entry.optString("question_label")) {
                    selectedId = id; showList = false; render()
                }
                block(row)
                if (scrollToCursor && i == listCursor) row.post { row.requestRectangleOnScreen(android.graphics.Rect(0, 0, row.width, row.height), true) }
            }
            return
        }
        val item = current()
        if (item == null) { block(text("判断待ちはありません")); return }
        val id = item.optString("id")
        val draft = drafts.getOrPut(id) { Draft() }
        block(text(item.optString("location"), 18f))
        if (item.optBoolean("locked")) {
            // Only the heading arrives until the biometric signature is accepted (PROTOCOL 3.4).
            block(text(decisionHeading(item.optString("raw_title"), "", item.optString("kind")), 28f))
            block(text("中身と選択肢は、顔・指紋で開くと見えます。", 18f))
            block(button("顔・指紋で開く", true) { unlock() })
            return
        }
        if (item.optString("question_label").isNotBlank()) block(text(item.optString("question_label"), 18f))
        val (heading, rest) = detailHeadingAndBody(item.optString("title"), item.optString("body"), item.optString("kind"))
        block(text(heading, 28f))
        val units = item.optJSONArray("approval_units")?.takeIf { it.length() > 0 }
        if (units != null) {
            item.optString("approval_units_heading").takeIf { it.isNotEmpty() }?.let { block(text(it, 20f).apply { setTypeface(typeface, Typeface.BOLD) }) }
            for (i in 0 until units.length()) units.optJSONObject(i)?.let { block(unitRow(it, i + 1), 6) }
        }
        for ((kind, value) in permissionDetails(item.optString("permission_explanation"), if (units != null) "" else item.optString("approval_command"), item.optString("permission_warning"), item.optString("permission_command_note"))) {
            if (kind == "コマンド") {
                block(text(value, 20f).apply { setTextColor(Color.RED); typeface = Typeface.MONOSPACE })
            }
            else block(text(value))
        }
        val details = item.optJSONArray("details") ?: JSONArray()
        for (i in 0 until details.length()) details.optJSONObject(i)?.let { block(text("${it.optString("label")}：${it.optString("value")}", 18f)) }
        if (item.optBoolean("machine_down")) block(text("出どころのマシンに接続できないため送れません", 18f).apply { setTextColor(warn) })
        if (item.optString("reauth_url").isNotEmpty()) block(text("SSH の再承認が必要です\n${item.optString("reauth_url")}", 18f).apply { setTextColor(warn) })
        if (item.optString("retry_error").isNotEmpty()) block(text(item.optString("retry_error"), 18f).apply { setTextColor(warn) })
        if (rest.isNotEmpty()) block(text(rest))
        val images = item.optJSONArray("images") ?: JSONArray()
        for (i in 0 until images.length()) addImage(images.optString(i, ""))
        val cards = item.optJSONArray("cards") ?: JSONArray()
        for (i in 0 until cards.length()) cards.optJSONObject(i)?.let { card ->
            addImage(card.optString("image")); block(text("${card.optString("title")}\n${card.optString("url")}"))
        }
        val videos = item.optJSONArray("videos") ?: JSONArray()
        for (i in 0 until videos.length()) videos.optJSONObject(i)?.let { video -> block(videoTile(video.optString("key"))) }
        val mediaErrors = item.optJSONArray("media_errors") ?: JSONArray()
        for (i in 0 until mediaErrors.length()) mediaErrors.optString(i).takeIf { it.isNotEmpty() }?.let { block(text(it, 18f).apply { setTextColor(warn) }) }
        val options = item.optJSONArray("options") ?: JSONArray()
        val allowText = item.optBoolean("allow_text")
        val slots = DetailCursor.slots(options.length(), allowText)
        val cursor = cursorOf(item, draft)
        val multiSelect = item.optBoolean("multi_select")
        if (multiSelect) block(text(multiSelectGuide(draft.selected.size), 18f), 4)
        for (i in 0 until options.length()) {
            val option = options.optJSONObject(i) ?: continue
            val optionId = option.optString("id")
            val description = optionSupplement(option.optString("description"), option.optString("preview"), option.optBoolean("preview_unavailable"))
            val row = optionRow(i, option.optString("label"), description, cursor == i, optionId in draft.selected, multiSelect) {
                if (draft.sequence == null) {
                    draft.cursor = CursorRef(Slot.OPTION, optionId)
                    if (multiSelect) toggle(draft, optionId) else { draft.selected.clear(); draft.selected.add(optionId) }
                    render()
                }
            }
            block(row)
            if (scrollToCursor && i == cursor) row.post { row.requestRectangleOnScreen(android.graphics.Rect(0, 0, row.width, row.height), true) }
        }
        if (item.optBoolean("allow_text")) {
            block(text("コメント（任意）", 18f), 4)
            val onComment = slots.getOrNull(cursor) == Slot.COMMENT
            val comment = input("コメントを入力", 3).apply {
                setText(draft.text); isEnabled = draft.sequence == null
                if (onComment) { background = frame(green).apply { setStroke(dp(5), green) }; setTextColor(Color.BLACK); setHintTextColor(Color.BLACK) }
            }
            commentView = comment
            comment.addTextChangedListener(object : TextWatcher {
                override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
                override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) { draft.text = s.toString() }
                override fun afterTextChanged(s: Editable?) {}
            })
            block(comment)
            if (scrollToCursor && onComment) comment.post { comment.requestRectangleOnScreen(android.graphics.Rect(0, 0, comment.width, comment.height), true) }
            block(voiceButton(id, draft))
            draft.images.forEachIndexed { index, _ -> block(button("添付画像 ${index + 1} ×") { draft.images.removeAt(index); render() }.apply { isEnabled = draft.sequence == null }) }
            val attachments = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
            pair(button("写真を選ぶ") { if (draft.images.size < 4) { pendingAttachmentId = id; startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).setType("image/*").addCategory(Intent.CATEGORY_OPENABLE), 2) } }.apply { isEnabled = draft.sequence == null },
                 button("カメラで撮る") { pendingAttachmentId = id; openCamera(draft) }.apply { isEnabled = draft.sequence == null }, attachments)
            block(attachments)
        }
        block(text(deadlineAndCount(item.optString("deadline"), item.optInt("queue_remaining")), 18f))
        footer.setPadding(0, dp(8), 0, dp(8))
        val laterFocused = slots.getOrNull(cursor) == Slot.LATER
        val answerFocused = slots.getOrNull(cursor) == Slot.ANSWER
        pair(button("あとで", false, laterFocused) { if (DecisionState.send(JSONObject().put("type", "later").put("id", id))) { showList = true; render() } },
             button(if (draft.uncertain) "保存状態を確認" else if (draft.sequence == null) answerLabel(item.optBoolean("multi_select"), draft.selected.size) else "送信中", true, answerFocused) { send(item, draft) }.apply { isEnabled = draft.sequence == null || draft.uncertain }, footer)
    }
    private fun optionRow(index: Int, label: String, description: String, focused: Boolean, checked: Boolean, multiSelect: Boolean = false, supplement: String = "", action: () -> Unit): View {
        val color = if (focused) Color.BLACK else green
        return LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; gravity = Gravity.CENTER_VERTICAL; minimumHeight = dp(64)
            background = frame(if (focused) green else Color.BLACK); setPadding(dp(16), dp(12), dp(16), dp(12))
            if (supplement.isNotBlank()) addView(text(supplement, 18f).apply { setTextIsSelectable(false); movementMethod = null; setTextColor(color) })
            val heading = text(optionHeading(index, label, multiSelect, checked)).apply { setTextIsSelectable(false); movementMethod = null; setTextColor(color) }
            addView(heading)
            if (description.isNotEmpty()) addView(text(description, 18f).apply { setTextIsSelectable(false); movementMethod = null; setTextColor(if (focused) Color.BLACK else dim) })
            setOnClickListener { action() }
        }
    }
    private val videoPreviews = mutableMapOf<String, VideoPreview>()
    private val loadingPreviews = mutableSetOf<String>()
    private val previewLoader = java.util.concurrent.Executors.newSingleThreadExecutor()
    private var videoViewer: VideoViewerDialog? = null
    /**
     * A received video shows its first frame with a play label; a tap opens the
     * full-screen player. The frame is read off the main thread, scaled like
     * images. Until every chunk arrives, the tile says it is receiving.
     */
    private fun videoTile(key: String): View {
        val file = DecisionState.videos[key]
        if (file == null) return text("動画を受け取り中…", 18f).apply { setTextColor(dim) }
        val preview = videoPreviews[key]
        if (preview == null && loadingPreviews.add(key)) previewLoader.execute {
            val read = VideoPreviews.read(file)
            runOnUiThread {
                loadingPreviews.remove(key)
                if (isDestroyed || DecisionState.videos[key] != file) return@runOnUiThread
                videoPreviews[key] = read; render()
            }
        }
        val tile = FrameLayout(this).apply { background = frame(); setPadding(dp(2), dp(2), dp(2), dp(2)) }
        val picture = ImageView(this).apply { adjustViewBounds = true; setBackgroundColor(Color.BLACK); preview?.frame?.let { setImageBitmap(it) } }
        tile.addView(picture, FrameLayout.LayoutParams(-1, if (preview?.frame == null) dp(160) else -2))
        val length = VideoPreviews.length(preview?.millis ?: 0L)
        val label = if (length.isEmpty()) "▶ 動画を再生" else "▶ 動画を再生  $length"
        tile.addView(TextView(this).apply {
            text = label
            textSize = 22f * textScale; setTextColor(Color.BLACK); setTypeface(typeface, Typeface.BOLD)
            background = GradientDrawable().apply { setColor(green); cornerRadius = dp(8).toFloat() }
            setPadding(dp(16), dp(10), dp(16), dp(10))
        }, FrameLayout.LayoutParams(-2, -2, Gravity.BOTTOM or Gravity.START).apply { setMargins(dp(12), dp(12), dp(12), dp(12)) })
        tile.contentDescription = label; tile.isClickable = true; tile.isFocusable = true
        tile.setOnClickListener {
            videoViewer?.dismiss()
            videoViewer = VideoViewerDialog(this, key, file).also { it.setOnDismissListener { if (videoViewer === it) videoViewer = null }; it.show() }
        }
        return tile
    }
    private var viewer: ImageViewerDialog? = null
    private fun showViewer(bitmap: android.graphics.Bitmap) {
        viewer?.dismiss()
        viewer = ImageViewerDialog(this, bitmap) { nav.ignoreNextUpRelease() }.also { it.setOnDismissListener { viewer = null }; it.show() }
    }
    private fun addImage(encoded: String) {
        if (encoded.isEmpty()) return
        try {
            val bytes = android.util.Base64.decode(encoded, android.util.Base64.NO_WRAP)
            val bounds = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
            android.graphics.BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
            var sample = 1
            while (bounds.outWidth / sample > 1600 || bounds.outHeight / sample > 1600) sample *= 2
            val bitmap = android.graphics.BitmapFactory.decodeByteArray(bytes, 0, bytes.size, android.graphics.BitmapFactory.Options().apply { inSampleSize = sample }) ?: return
            block(ImageView(this).apply { setImageBitmap(bitmap); adjustViewBounds = true; setOnClickListener { showViewer(bitmap) } })
        } catch (_: Exception) { /* Display failures do not prevent answering. */ }
    }
    private val voice by lazy { VoiceRecorder(this) }
    private var voiceDecision: String? = null
    private var transcribing = false
    private fun voiceButton(id: String, draft: Draft): View {
        val label = when {
            transcribing && voiceDecision == id -> "文字にしています…"
            voice.recording && voiceDecision == id -> "■ 止めて文字にする"
            else -> "🎤 話して入力"
        }
        return button(label, voice.recording && voiceDecision == id) { toggleVoice(id, draft) }.apply {
            isEnabled = draft.sequence == null && !transcribing && (!voice.recording || voiceDecision == id)
        }
    }
    private fun toggleVoice(id: String, draft: Draft) {
        if (voice.recording) {
            val audio = voice.stop()
            if (audio == null) { voiceDecision = null; Toast.makeText(this, "録音できませんでした", Toast.LENGTH_LONG).show(); render(); return }
            transcribing = true
            render()
            val settings = SpeechSettings(this)
            Thread {
                val result = runCatching { Transcriber.transcribe(settings, audio) }
                audio.delete()
                runOnUiThread {
                    transcribing = false
                    voiceDecision = null
                    result.onSuccess { text ->
                        if (draft.sequence != null) Toast.makeText(this, "送った後に文字起こしが終わったので、コメントには入れませんでした", Toast.LENGTH_LONG).show()
                        else if (text.isEmpty()) Toast.makeText(this, "声を聞き取れませんでした", Toast.LENGTH_LONG).show()
                        else draft.text = Transcriber.append(draft.text, text)
                    }.onFailure { error -> Toast.makeText(this, error.message ?: "文字起こしに失敗しました", Toast.LENGTH_LONG).show() }
                    render()
                }
            }.start()
            return
        }
        if (SpeechSettings.endpoint(SpeechSettings(this).url).isFailure) {
            Toast.makeText(this, "接続設定で、音声の文字起こしの接続先を入れてください", Toast.LENGTH_LONG).show(); return
        }
        if (checkSelfPermission(android.Manifest.permission.RECORD_AUDIO) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(android.Manifest.permission.RECORD_AUDIO), 3); return
        }
        try { voice.start(); voiceDecision = id } catch (error: Exception) { Toast.makeText(this, "録音を始められませんでした: ${error.message}", Toast.LENGTH_LONG).show() }
        render()
    }
    override fun onPause() {
        if (voice.recording) {
            voice.cancel(); voiceDecision = null
            Toast.makeText(this, "アプリから離れたので、録音を止めました", Toast.LENGTH_SHORT).show()
            render()
        }
        super.onPause()
    }
    private val pairing by lazy { Pairing(this) { settingsVisible = false; render() } }
    private fun unlock() = Unlock.run(this) { message -> Toast.makeText(this, message, Toast.LENGTH_LONG).show() }
    override fun onResume() {
        super.onResume()
        // Opening the app asks for a face or fingerprint before showing what the PCs sent (⑤).
        unlock()
    }
    private fun renderMachines() {
        block(text("PC（中継でつなぐ）", 22f), 4)
        val machines = MachineStore(this).list()
        if (machines.isEmpty()) block(text("まだ登録した PC はありません。PC の端末で `node apps/android/bridge/main.mjs pair` を実行し、出た QR を読んでください。", 16f), 4)
        machines.forEach { machine ->
            val state = DecisionState.sources[machine.id]
            val status = when { state == null || !state.connected -> "未接続"; state.operable -> "接続中・操作できる"; else -> "接続中・顔と指紋で開く前" }
            val row = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
            pair(text("${machine.name}\n$status", 18f), button("外す") {
                android.app.AlertDialog.Builder(this).setTitle("${machine.name} を外しますか？")
                    .setMessage("この端末から ${machine.name} の登録を消します。PC 側の登録も外すには、PC で devices と revoke を実行してください。")
                    .setPositiveButton("外す") { _, _ -> MachineStore(this).remove(machine.id); startService(Intent(this, DecisionService::class.java)); render() }
                    .setNegativeButton("やめる", null).show()
            }, row)
            block(row)
        }
        block(text("外出中など QR を読めないときは、下の「鍵を作る」で出た1行を PC の持ち主へ送り、返ってきた接続情報を貼ってください。秘密鍵はこの端末から出ません。", 16f), 4)
        block(button("鍵を作る（公開鍵を出す）") {
            try {
                val store = MachineStore(this)
                val line = ManualLink.publicKeys(store, android.os.Build.MODEL)
                // PC は登録した鍵の指紋を出す。同じ指紋なら、途中で差し替えられていない。
                val view = text("指紋: ${RelayMath.fingerprint(store.noisePublicKey())}\n\n$line", 14f).apply { setTextIsSelectable(true); typeface = Typeface.MONOSPACE }
                android.app.AlertDialog.Builder(this).setTitle("この端末の公開鍵").setView(view)
                    .setPositiveButton("コピー") { _, _ ->
                        (getSystemService(Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager).setPrimaryClip(android.content.ClipData.newPlainText("ikesu 公開鍵", line))
                        Toast.makeText(this, "コピーしました。Slack などで送ってください", Toast.LENGTH_LONG).show()
                    }.setNegativeButton("閉じる", null).show()
            } catch (error: Exception) { Toast.makeText(this, "鍵を作れませんでした。端末に指紋か顔を登録してください（${error.javaClass.simpleName}）", Toast.LENGTH_LONG).show() }
        })
        val linkField = input("接続情報（{\"v\":1,\"kind\":\"ikesu-link\",…}）を貼る", 3)
        block(linkField)
        block(button("接続情報で PC を追加") {
            try {
                val machine = ManualLink.parse(linkField.text.toString())
                MachineStore(this).add(machine)
                startService(Intent(this, DecisionService::class.java).setAction(DecisionService.RECONNECT))
                android.app.AlertDialog.Builder(this).setTitle("${machine.name} を追加しました")
                    .setMessage("PC の鍵の指紋: ${RelayMath.fingerprint(B64.decode(machine.bridge))}\nPC 側に出た指紋と同じか確かめてください。違うときは「外す」で消してください。")
                    .setPositiveButton("閉じる", null).show()
                render()
            } catch (error: Exception) { Toast.makeText(this, error.message ?: "接続情報を読めませんでした", Toast.LENGTH_LONG).show() }
        })
        block(button("PC を追加（QR を読む）", true) {
            if (checkSelfPermission(android.Manifest.permission.CAMERA) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(android.Manifest.permission.CAMERA), 5); return@button
            }
            com.google.zxing.integration.android.IntentIntegrator(this).setDesiredBarcodeFormats(com.google.zxing.integration.android.IntentIntegrator.QR_CODE)
                .setPrompt("PC の端末に出た QR を読んでください").setBeepEnabled(false).setOrientationLocked(false).initiateScan()
        })
    }
    private fun renderSettings() {
        block(text("接続設定", 28f))
        renderMachines()
        val speech = SpeechSettings(this)
        block(text("音声の文字起こし", 22f), 4)
        block(text("話した答えを文字にする、OpenAI 互換の文字起こしの接続先（例: http://ubuntu-3090:8002）。音声はここへだけ送ります。", 16f), 4)
        val speechUrl = input("http://接続先:8002").apply { setText(speech.url) }
        block(speechUrl)
        block(text("モデル名", 18f), 4)
        val speechModel = input(SpeechSettings.DEFAULT_MODEL).apply { setText(speech.model) }
        block(speechModel)
        block(button("音声の設定を保存") {
            val value = speechUrl.text.toString().trim()
            val checked = if (value.isEmpty()) Result.success("") else SpeechSettings.endpoint(value)
            checked.onSuccess { speech.url = value; speech.model = speechModel.text.toString().trim(); Toast.makeText(this, "保存しました", Toast.LENGTH_SHORT).show() }
                .onFailure { error -> Toast.makeText(this, error.message, Toast.LENGTH_LONG).show() }
        })
    }
    private fun toggle(draft: Draft, id: String) { if (!draft.selected.add(id)) draft.selected.remove(id) }
    private fun move(delta: Int) {
        if (showList) {
            val count = DecisionState.decisions.length()
            if (count > 0) { listCursor = (listCursor + delta + count) % count; render(scrollToCursor = true) }
            return
        }
        val item = current() ?: return
        val count = DetailCursor.slots(item.optJSONArray("options")?.length() ?: 0, item.optBoolean("allow_text")).size
        val draft = drafts.getOrPut(item.optString("id")) { Draft() }
        var index = cursorOf(item, draft)
        if (draft.cursorNavigated || delta < 0) index = (index + delta + count) % count
        draft.cursor = DetailCursor.refAt(index, optionIds(item), item.optBoolean("allow_text"))
        draft.cursorNavigated = true
        render(scrollToCursor = true)
    }
    private fun confirm() {
        if (showList) {
            DecisionState.decisions.optJSONObject(listCursor)?.let { selectedId = it.optString("id"); showList = false; render() }
            return
        }
        val item = current() ?: return
        val draft = drafts.getOrPut(item.optString("id")) { Draft() }
        if (draft.sequence != null) return
        val options = item.optJSONArray("options") ?: JSONArray()
        val slot = DetailCursor.slots(options.length(), item.optBoolean("allow_text")).getOrNull(cursorOf(item, draft)) ?: return
        when (slot) {
            Slot.ANSWER -> { send(item, draft); return }
            Slot.LATER -> { if (DecisionState.send(JSONObject().put("type", "later").put("id", item.optString("id")))) { showList = true; render() }; return }
            Slot.COMMENT -> { commentView?.let { it.requestFocus(); (getSystemService(Context.INPUT_METHOD_SERVICE) as android.view.inputmethod.InputMethodManager).showSoftInput(it, 0) }; return }
            Slot.OPTION -> {}
        }
        val id = options.optJSONObject(cursorOf(item, draft))?.optString("id") ?: return
        if (item.optBoolean("multi_select")) { toggle(draft, id); render() }
        else { draft.selected.clear(); draft.selected.add(id); send(item, draft) }
    }
    private fun send(item: JSONObject, draft: Draft) {
        if (draft.sequence != null && !draft.uncertain) return
        if (voice.recording || transcribing) { Toast.makeText(this, "話した内容を文字にし終わってから送ってください", Toast.LENGTH_LONG).show(); return }
        val sequence = draft.sequence ?: UUID.randomUUID().toString()
        if (!DecisionState.answer(item.optString("id"), draft.selected.toList(), draft.text, draft.images, sequence)) { Toast.makeText(this, "接続されていません", Toast.LENGTH_LONG).show(); return }
        draft.sequence = sequence
        draft.uncertain = false
        draft.submittedItem = item
        render()
    }
    override fun dispatchTouchEvent(ev: MotionEvent): Boolean {
        scaleDetector.onTouchEvent(ev)
        if (scaling) {
            if (ev.actionMasked == MotionEvent.ACTION_UP || ev.actionMasked == MotionEvent.ACTION_CANCEL) scaling = false
            return true
        }
        return super.dispatchTouchEvent(ev)
    }
    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (editorFocused() && (keyCode == VolumeNav.UP || keyCode == VolumeNav.DOWN)) return super.onKeyDown(keyCode, event)
        if (nav.keyDown(keyCode, event.repeatCount == 0)) { event.startTracking(); return true }
        return super.onKeyDown(keyCode, event)
    }
    override fun onKeyLongPress(keyCode: Int, event: KeyEvent): Boolean = !editorFocused() && nav.keyLongPress(keyCode) || super.onKeyLongPress(keyCode, event)
    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean = !editorFocused() && nav.keyUp(keyCode) || super.onKeyUp(keyCode, event)
    private fun openCamera(draft: Draft) {
        if (draft.images.size >= 4) return
        pendingCamera = contentResolver.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, android.content.ContentValues().apply { put(MediaStore.Images.Media.DISPLAY_NAME, "ikesu-${UUID.randomUUID()}.jpg"); put(MediaStore.Images.Media.MIME_TYPE, "image/jpeg") })
        pendingCamera?.let { startActivityForResult(Intent(MediaStore.ACTION_IMAGE_CAPTURE).putExtra(MediaStore.EXTRA_OUTPUT, it).addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION), 3) }
    }
    @Deprecated("Android activity result callback")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        com.google.zxing.integration.android.IntentIntegrator.parseActivityResult(requestCode, resultCode, data)?.let { scan ->
            if (requestCode == com.google.zxing.integration.android.IntentIntegrator.REQUEST_CODE) { scan.contents?.let { pairing.scanned(it) }; return }
        }
        val uri = if (requestCode == 3) pendingCamera else data?.data
        if (resultCode == RESULT_OK && uri != null) {
            try {
                val bytes = contentResolver.openInputStream(uri)?.use { it.readNBytes(20 * 1024 * 1024 + 1) } ?: error("画像を開けません")
                require(bytes.size <= 20 * 1024 * 1024) { "画像は1枚20MBまでです" }
                pendingAttachmentId?.let { drafts[it]?.images?.add(bytes) }
            } catch (error: Exception) { Toast.makeText(this, error.message, Toast.LENGTH_LONG).show() }
        }
        if (requestCode == 3 && uri != null) contentResolver.delete(uri, null, null)
        pendingCamera = null; pendingAttachmentId = null; render()
    }
}
