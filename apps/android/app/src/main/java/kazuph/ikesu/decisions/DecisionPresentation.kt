package kazuph.ikesu.decisions

fun decisionHeading(title: String, body: String, kind: String): String =
    title.trim().ifEmpty { body.lineSequence().firstOrNull()?.trim().orEmpty() }.ifEmpty {
        when (kind) {
            "command_guard" -> "コマンドの承認"
            "tool_permission" -> "ツールの許可"
            "agent_prompt" -> "エージェントの質問"
            "ask" -> "確認"
            else -> "判断待ち"
        }
    }

/**
 * The detail's big heading and the smaller text under it, without saying the
 * same sentence twice. The bridge already drops a title that only repeats the
 * question (shared with the Mac panel and Slack); then the question's first
 * line becomes the heading and the rest stays below.
 */
fun detailHeadingAndBody(title: String, body: String, kind: String): Pair<String, String> {
    val normalize = { text: String -> text.replace(Regex("\\s+"), " ").trim() }
    if (title.isNotBlank()) {
        val head = normalize(title)
        if (normalize(body) == head) return title.trim() to ""
        if (normalize(body).startsWith(head)) {
            // Drop the leading copy of the title from the body.
            var consumed = 0
            var matched = 0
            val target = head.replace(" ", "")
            while (consumed < body.length && matched < target.length) {
                if (!body[consumed].isWhitespace()) matched++
                consumed++
            }
            return title.trim() to body.substring(consumed).trimStart()
        }
        return title.trim() to body
    }
    val lines = body.trim().lines()
    val first = lines.firstOrNull()?.trim().orEmpty()
    if (first.isEmpty()) return decisionHeading("", "", kind) to ""
    return first to lines.drop(1).joinToString("\n").trim()
}

fun optionSupplement(description: String, preview: String, previewUnavailable: Boolean): String =
    listOf(description, preview, if (previewUnavailable) "画面で確認" else "")
        .filter { it.isNotEmpty() }.joinToString("\n")

fun deadlineAndCount(deadline: String, remaining: Int): String =
    "${deadline.ifEmpty { "期限なし" }}　｜　ほか ${remaining} 件"

fun permissionDetails(explanation: String, command: String, warning: String, note: String = ""): List<Pair<String, String>> =
    listOf("理由" to explanation, "注記" to note, "コマンド" to command, "警告" to warning).filter { it.second.isNotEmpty() }

fun isFlaggedVerdict(verdict: String): Boolean = verdict == "ask" || verdict == "block"

fun unitJoiner(op: String): String = if (op == "\n") "⏎" else op

/** Red lines under a flagged command row: where it hit (target), then why and which rule. */
fun unitNotes(target: String, reason: String, rule: String): List<String> = buildList {
    if (target.isNotEmpty()) add("中で: $target")
    if (reason.isNotEmpty() || rule.isNotEmpty()) {
        add("理由: " + reason + (if (reason.isNotEmpty() && rule.isNotEmpty()) " ／ " else "") + (if (rule.isEmpty()) "" else "規則 $rule"))
    }
}

fun unitAllowNote(verdict: String): String = if (verdict == "unjudged") "未判定" else "確認なし"

/** Option heading: multi-select rows carry a checkbox, single-select rows a check on the chosen one. */
fun optionHeading(index: Int, label: String, multiSelect: Boolean, checked: Boolean): String =
    if (multiSelect) "${if (checked) "☑" else "☐"} ${index + 1}. $label" else "${if (checked) "✓ " else ""}${index + 1}. $label"

/** Shown above the options of a multi-select decision, so it is not mistaken for a single choice. */
fun multiSelectGuide(selected: Int): String =
    "複数選べます。選んだら「回答する」で送ります" + if (selected > 0) "（${selected}つ選択中）" else ""

/** The answer button names how many choices a multi-select answer carries. */
fun answerLabel(multiSelect: Boolean, selected: Int): String =
    if (multiSelect && selected > 0) "回答する（${selected}つ）" else "回答する"

/** 通知を出す判断待ち（notify=true）の id。通知が許可されていなければ空（届いたとは言えない）。 */
fun notifiedIds(items: List<Pair<String, Boolean>>, notificationsEnabled: Boolean): List<String> =
    if (!notificationsEnabled) emptyList() else items.filter { it.second && it.first.isNotEmpty() }.map { it.first }.distinct()

/** What to do with one decision's notification when a new list arrives. */
enum class NotifyAction { ALERT, SILENT, CANCEL, KEEP }

/**
 * A decision rings (sound and pop-up) only once. The PC hides it (notify=false) while one of its
 * screens shows that space; when the PC stops showing it, the notification comes back without
 * ringing, so switching spaces or moving the focus on the PC does not interrupt the phone.
 * notify: this list asks for a notification. shownBefore: the previous list asked for one too.
 * alerted: this decision already rang on this phone.
 */
fun notifyAction(notify: Boolean, shownBefore: Boolean, alerted: Boolean): NotifyAction = when {
    !notify -> NotifyAction.CANCEL
    shownBefore -> NotifyAction.KEEP
    alerted -> NotifyAction.SILENT
    else -> NotifyAction.ALERT
}

/** Whether two lists show the same on screen: only the notify flag (whether the PC shows it) may differ. */
fun sameForDisplay(old: List<org.json.JSONObject>, new: List<org.json.JSONObject>): Boolean {
    if (old.size != new.size) return false
    val strip = { item: org.json.JSONObject -> org.json.JSONObject(item.toString()).apply { remove("notify") }.toString() }
    return old.indices.all { strip(old[it]) == strip(new[it]) }
}

/**
 * The choices a notification (and so a Pixel Watch) may offer as buttons: only while the phone is
 * unlocked with face or fingerprint for that PC (operable), for a decision whose options arrived,
 * answered with a single choice. Otherwise null: the notification only opens the app to unlock.
 */
fun notificationChoices(item: org.json.JSONObject, operable: Boolean): List<Pair<String, String>>? {
    if (!operable || item.optBoolean("locked") || item.optBoolean("multi_select")) return null
    val options = item.optJSONArray("options") ?: return null
    val choices = (0 until options.length()).mapNotNull { options.optJSONObject(it) }
        .map { it.optString("id") to it.optString("label") }
        .filter { it.first.isNotEmpty() && it.second.isNotEmpty() }
    return choices.takeIf { it.isNotEmpty() }
}

/** What a decision's notification shows: heading, place and buttons. An update is due when it differs. */
fun notificationContent(item: org.json.JSONObject, operable: Boolean): String = listOf(
    decisionHeading(item.optString("raw_title"), item.optString("raw_body"), item.optString("kind")),
    item.optString("location"),
    notificationChoices(item, operable)?.joinToString("\u0001") { "${it.first}\u0002${it.second}" } ?: "",
).joinToString("\u0000")

/** The data of a notification answer button: different for every decision and choice. */
fun answerData(decision: String, option: String): String =
    "ikesu-answer:" + java.net.URLEncoder.encode(decision, "UTF-8") + "/" + java.net.URLEncoder.encode(option, "UTF-8")
