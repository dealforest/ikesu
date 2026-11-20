package kazuph.ikesu.decisions

import android.app.Activity
import android.app.AlertDialog
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.graphics.Typeface
import android.net.Uri
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast

internal fun showTerminalLongPressMenu(
    activity: Activity,
    content: PressedTerminalContent?,
    actions: List<Pair<String, () -> Unit>>,
) {
    val items = mutableListOf<Pair<String, () -> Unit>>()
    content?.url?.let { url ->
        items += "リンクを開く" to { openTerminalLink(activity, url) }
        items += "リンクをコピー" to { copyTerminalText(activity, "リンク", url) }
    }
    content?.text?.takeIf { it.isNotBlank() }?.let { value ->
        items += "ペインの文字をコピー" to { showSelectableTerminalText(activity, value) }
    }
    items += actions
    AlertDialog.Builder(activity).setItems(items.map { it.first }.toTypedArray()) { _, which ->
        items[which].second()
    }.show()
}

private fun openTerminalLink(activity: Activity, url: String) {
    val uri = Uri.parse(url)
    if ((uri.scheme != "http" && uri.scheme != "https") || uri.host.isNullOrBlank()) return
    try {
        activity.startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE))
    } catch (_: ActivityNotFoundException) {
        Toast.makeText(activity, "リンクを開けるアプリがありません", Toast.LENGTH_SHORT).show()
    }
}

private fun showSelectableTerminalText(activity: Activity, value: String) {
    val margin = (20 * activity.resources.displayMetrics.density).toInt()
    val selectable = TextView(activity).apply {
        text = value
        textSize = 18f
        typeface = Typeface.MONOSPACE
        setTextIsSelectable(true)
        setPadding(margin, margin / 2, margin, margin)
    }
    val scroll = ScrollView(activity).apply { addView(selectable) }
    AlertDialog.Builder(activity).setTitle("ペインの文字").setView(scroll)
        .setNegativeButton("閉じる", null)
        .setPositiveButton("すべてコピー") { _, _ ->
            copyTerminalText(activity, "ペインの文字", value)
        }.show()
}

private fun copyTerminalText(activity: Activity, label: String, value: String) {
    activity.getSystemService(ClipboardManager::class.java)
        .setPrimaryClip(ClipData.newPlainText(label, value))
}
