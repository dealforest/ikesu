package kazuph.ikesu.decisions

import kazuph.ikesu.R

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.net.ConnectivityManager
import android.net.Network
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import android.util.Base64

/** One PC paired through the relay: where its decisions come from. */
class DecisionSource(val id: String, val name: String) {
    var decisions = JSONArray()
    var connected = false
    /** Relay only: the bridge accepted the operate signature (PROTOCOL 3.3). */
    var operable = false
    var link: RelayLink? = null
    var beat = 0L

    fun send(value: JSONObject): Boolean = link?.send(value) == true
}

/** Decisions from every source, merged for the screens. Ids are "<source>~<id>". Use on the main thread. */
object DecisionState : AnswerChannel {
    @Volatile var decisions = JSONArray()
    @Volatile var connected = false
    @Volatile var result: JSONObject? = null
    val sources = linkedMapOf<String, DecisionSource>()
    val videos = mutableMapOf<String, File>()

    fun key(source: String, id: String) = "$source~$id"
    fun split(key: String): Pair<String, String>? = key.indexOf('~').takeIf { it > 0 }?.let { key.substring(0, it) to key.substring(it + 1) }

    fun recompute() {
        val merged = JSONArray()
        sources.values.forEach { source -> for (i in 0 until source.decisions.length()) merged.put(source.decisions.get(i)) }
        decisions = merged
        connected = sources.values.any { it.connected }
    }

    /** Sends a message about one decision to the PC it came from. */
    fun send(value: JSONObject): Boolean {
        val (source, id) = split(value.optString("id")) ?: return false
        return sources[source]?.send(JSONObject(value.toString()).put("id", id)) == true
    }

    /** Tells each PC which of its decisions were shown as notifications. */
    fun shown(keys: List<String>): Boolean {
        var ok = true
        keys.mapNotNull { split(it) }.groupBy({ it.first }, { it.second }).forEach { (source, ids) ->
            ok = (sources[source]?.send(JSONObject().put("type", "shown").put("ids", JSONArray(ids))) == true) && ok
        }
        return ok && keys.isNotEmpty()
    }

    override fun answer(decisionId: String, optionIds: List<String>, text: String, images: List<ByteArray>, sequence: String): Boolean {
        val (source, id) = split(decisionId) ?: return false
        return sources[source]?.send(JSONObject().put("type", "answer").put("id", id).put("sequence", sequence)
            .put("options", JSONArray(optionIds)).put("text", text)
            .put("images", JSONArray(images.map { Base64.encodeToString(it, Base64.NO_WRAP) }))) == true
    }

    /** Relay connections that still need the operate signature. */
    fun lockedLinks(): List<DecisionSource> = sources.values.filter { it.link?.ready == true && !it.operable }

    /** Sends one biometric-backed signature over every listed handshake (PROTOCOL 3.3). */
    fun operate(targets: List<DecisionSource>, hashes: List<ByteArray>, signature: ByteArray) {
        val message = JSONObject().put("type", "operate").put("hashes", JSONArray(hashes.map { B64.encode(it) })).put("sig", B64.encode(signature))
        targets.forEach { it.link?.send(message) }
    }

    fun locked(): Boolean = sources.values.any { it.connected && !it.operable }

    /** Whether an answer for this decision can reach its PC now (a relay PC also needs the operate signature). */
    fun reachable(key: String): Boolean {
        val source = split(key)?.let { sources[it.first] } ?: return false
        return source.connected && source.operable
    }

    /** Keeps only the heading of each decision: what may stay visible without the biometric unlock (PROTOCOL 3.4). */
    fun headingsOnly(items: JSONArray): JSONArray {
        val out = JSONArray()
        for (index in 0 until items.length()) {
            val item = items.optJSONObject(index) ?: continue
            out.put(JSONObject().put("id", item.optString("id")).put("source", item.optString("source"))
                .put("raw_title", item.optString("raw_title")).put("kind", "agent_prompt").put("locked", true)
                .put("notify", item.optBoolean("notify")).put("location", item.optString("location")).put("options", JSONArray()))
        }
        return out
    }
}

class DecisionService : Service() {
    companion object {
        const val CHANGED = "kazuph.ikesu.CHANGED"
        const val RECONNECT = "kazuph.ikesu.RECONNECT"
        private const val ANSWER = "kazuph.ikesu.ANSWER"
        private const val CONNECTION = "connection"
        private const val QUESTIONS = "questions"
        private const val QUESTIONS_QUIET = "questions_quiet"
        private const val FOREGROUND_ID = 1
        private const val RETRY_MS = 2000L
        private const val RETRY_MAX_MS = 60_000L
    }
    private val handler = Handler(Looper.getMainLooper())
    private val notificationIds = mutableMapOf<String, Int>()
    private var nextNotification = 2
    /** Decisions that already rang on this phone (sound and pop-up happen once per decision). */
    private val alerted = mutableSetOf<String>()
    /** The choice buttons each shown notification carries now (null: none, it opens the app). */
    private val posted = mutableMapOf<String, String>()
    /** Answers sent from a notification button, by sequence, waiting for the PC's result. */
    private val notificationAnswers = mutableMapOf<String, String>()
    private var generation = 0
    private val receiving = mutableMapOf<String, Pair<FileOutputStream, Int>>()
    /** Failed attempts in a row per source, for a growing wait before the next try. */
    private val retries = mutableMapOf<String, Int>()
    private val manager by lazy { getSystemService(NotificationManager::class.java) }
    private val connectivity by lazy { getSystemService(ConnectivityManager::class.java) }
    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { handler.post { connect() } }
    }

    override fun onCreate() {
        super.onCreate()
        manager.createNotificationChannel(NotificationChannel(CONNECTION, "接続状態", NotificationManager.IMPORTANCE_LOW))
        manager.createNotificationChannel(NotificationChannel(QUESTIONS, "判断待ち", NotificationManager.IMPORTANCE_HIGH))
        manager.createNotificationChannel(NotificationChannel(QUESTIONS_QUIET, "判断待ち（再表示・音なし）", NotificationManager.IMPORTANCE_LOW))
        startForeground(FOREGROUND_ID, connectionNotification())
        // The passphrase connection is gone: remove what the old version stored (address, encrypted passphrase, its key).
        getSharedPreferences("connection", MODE_PRIVATE).edit().clear().apply()
        try { java.security.KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry("ikesu-decisions-token") } catch (_: Exception) { }
        connectivity.registerDefaultNetworkCallback(networkCallback)
    }
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // A notification button answers over the open connections: reconnecting would end the unlock.
        if (intent?.action == ANSWER) {
            val key = intent.getStringExtra("decision"); val option = intent.getStringExtra("option")
            if (key != null && option != null) answerFromNotification(key, option)
            return START_STICKY
        }
        connect()
        return START_STICKY
    }
    private fun changed() {
        DecisionState.recompute()
        manager.notify(FOREGROUND_ID, connectionNotification())
        sendBroadcast(Intent(CHANGED).setPackage(packageName))
    }
    /** Opens [intent] (the decisions panel) over the PC's screen, the app's first page: the screen stays open underneath. */
    private fun panel(code: Int, intent: Intent): PendingIntent = PendingIntent.getActivities(this, code,
        arrayOf(Intent(this, ScreenActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK), intent),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    private fun connectionNotification(): Notification = Notification.Builder(this, CONNECTION)
        .setSmallIcon(R.drawable.ic_notification).setContentTitle("ikesu")
        .setContentText("${if (DecisionState.connected) "接続中" else "未接続"} · 未回答 ${DecisionState.decisions.length()}件")
        .setColor(0xFF33FF66.toInt())
        .setOngoing(true).setContentIntent(panel(0, Intent(this, MainActivity::class.java).putExtra("list", true))).build()

    /** (Re)opens a connection to each paired PC. */
    private fun connect() {
        val thisGeneration = ++generation
        // The lists stay until each PC sends a fresh one, so a reconnect keeps the open decision and the drafts.
        val previous = DecisionState.sources.toMap()
        // Reconnecting ends every screen: it must not stay frozen on the old contents.
        previous.keys.forEach { id -> ScreenBus.listener?.invoke(id, JSONObject().put("type", "operate_expired")) }
        previous.values.forEach { it.link?.close("つなぎ直します") }
        DecisionState.sources.clear()
        receiving.forEach { (key, value) -> value.first.close(); File(cacheDir, "video-$key.partial").delete() }
        receiving.clear()
        retries.clear()
        val store = MachineStore(this)
        store.list().forEach { connectRelay(thisGeneration, store, it, previous[it.id]) }
        // Every connection is locked again: shown notifications lose their buttons now, and those of a
        // removed PC go away, without waiting for a list that an offline or removed PC never sends.
        DecisionState.recompute()
        refreshShown()
        changed()
    }

    /** Brings every shown decision notification in line with the current lists, without ringing. */
    private fun refreshShown() {
        for (key in posted.keys.toList()) {
            val source = DecisionState.split(key)?.let { DecisionState.sources[it.first] }
            val item = source?.let { jsonItems(it.decisions).firstOrNull { item -> item.optString("id") == key } }
            if (source == null || item == null) { notificationIds[key]?.let(manager::cancel); posted.remove(key); continue }
            if (posted[key] != notificationContent(item, source.operable)) postDecision(key, item, source.operable, quietUpdate = true)
        }
    }

    private fun connectRelay(thisGeneration: Int, store: MachineStore, machine: Machine, previous: DecisionSource?) {
        val source = DecisionSource(machine.id, machine.name)
        // Until the new connection is unlocked, only the headings stay visible.
        previous?.let { source.decisions = DecisionState.headingsOnly(it.decisions) }
        DecisionState.sources[source.id] = source
        val session = try { NoiseSession(store.noisePrivateKey(), B64.decode(machine.bridge), B64.decode(machine.room)) } catch (_: Exception) { return }
        val link = RelayLink(RelayLink.appUrl(machine), machine.tag, session, object : RelayLink.Listener {
            override fun onReady(link: RelayLink) { handler.post { if (thisGeneration == generation) { source.connected = true; retries.remove(source.id); changed() } } }
            override fun onMessage(link: RelayLink, message: JSONObject) { handler.post { if (thisGeneration == generation) handle(source, message) } }
            override fun onClosed(link: RelayLink, reason: String) {
                android.util.Log.w("IkesuDecisions", "${machine.name}: $reason")
                lost(thisGeneration, source, quick = link.bridgeAway)
            }
        })
        source.link = link
        link.start()
    }

    /** quick: the PC side was only away (restarting, asleep); try again in 2 seconds without growing the wait. */
    private fun lost(previous: Int, source: DecisionSource, quick: Boolean = false) {
        handler.post {
            if (previous != generation || DecisionState.sources[source.id] !== source) return@post
            source.connected = false
            // Find the half-received videos before the list is cut down to headings (which drops the video keys).
            dropPartialVideos(source)
            lock(source)
            changed()
            val attempt = if (quick) 0 else retries.getOrDefault(source.id, 0)
            retries[source.id] = attempt + 1
            handler.postDelayed({
                if (previous != generation || DecisionState.sources[source.id] !== source) return@postDelayed
                val store = MachineStore(this)
                store.list().firstOrNull { it.id == source.id }?.let { connectRelay(previous, store, it, source) }
            }, minOf(RETRY_MAX_MS, RETRY_MS shl minOf(attempt, 5)))
        }
    }

    /** Leaves only the headings of a relay PC's decisions and deletes their received videos. */
    private fun lock(source: DecisionSource) {
        source.operable = false
        // An open screen must not keep showing the PC once the unlock is over or the link is gone.
        ScreenBus.listener?.invoke(source.id, JSONObject().put("type", "operate_expired"))
        val keys = (0 until source.decisions.length()).flatMap { index ->
            val videos = source.decisions.optJSONObject(index)?.optJSONArray("videos") ?: JSONArray()
            (0 until videos.length()).mapNotNull { videos.optJSONObject(it)?.optString("key") }
        }
        keys.forEach { key -> DecisionState.videos.remove(key)?.delete() }
        source.decisions = DecisionState.headingsOnly(source.decisions)
        refreshChoices(source)
        DecisionState.recompute()
    }

    /** A video half received when the connection ended can never finish: the next one starts again from 0. */
    private fun dropPartialVideos(source: DecisionSource) {
        val keys = (0 until source.decisions.length()).flatMap { index ->
            val videos = source.decisions.optJSONObject(index)?.optJSONArray("videos") ?: JSONArray()
            (0 until videos.length()).mapNotNull { videos.optJSONObject(it)?.optString("key") }
        }.toSet()
        (receiving.keys intersect keys).forEach { key -> receiving.remove(key)?.first?.close(); File(cacheDir, "video-$key.partial").delete() }
    }

    private fun handle(source: DecisionSource, message: JSONObject) {
        val type = message.optString("type")
        // The open screen of this PC also needs these (PROTOCOL 4).
        if (type.startsWith("screen_") || type == "operate_expired" || type == "error") ScreenBus.listener?.invoke(source.id, message)
        // No screen is showing (the app went to the background while it opened): close it on the PC.
        if ((type == "screen_opened" || type == "screen_data") && ScreenBus.listener == null)
            source.send(JSONObject().put("type", "screen_close").put("screen", message.optInt("screen")))
        if (type.startsWith("screen_")) return
        when (type) {
            "snapshot" -> { val was = source.connected; source.connected = true; retries.remove(source.id); updateDecisions(source, message.optJSONArray("decisions") ?: JSONArray(), false, was) }
            "notice" -> { val was = source.connected; source.connected = true; if (!source.operable) updateDecisions(source, message.optJSONArray("decisions") ?: JSONArray(), true, was) else if (!was) changed() }
            "operate_ok" -> { source.operable = true; changed() }
            "operate_expired" -> { lock(source); changed() }
            "beat" -> source.beat = message.optLong("n")
            "answer_result", "error" -> {
                notificationAnswerResult(message)
                DecisionState.result = message
                if (message.optString("message") == "operate required") lock(source)
                changed()
            }
            "video_chunk" -> receiveVideo(source, message)
            "video_end" -> finishVideo(message)
        }
    }

    /** Stores a source's decisions under prefixed ids. A notice (no operate yet) carries only the heading. */
    private fun updateDecisions(source: DecisionSource, raw: JSONArray, notice: Boolean, connectedBefore: Boolean) {
        val items = JSONArray()
        for (index in 0 until raw.length()) {
            val original = raw.optJSONObject(index) ?: continue
            val id = original.optString("id").takeIf { it.isNotEmpty() } ?: continue
            val item = if (notice) {
                val origin = original.opt("origin")
                val place = when (origin) {
                    is JSONObject -> listOfNotNull(origin.optString("workspace_label").takeIf { it.isNotEmpty() }, origin.optString("pane_id").takeIf { it.isNotEmpty() })
                    is String -> listOfNotNull(origin.takeIf { it.isNotEmpty() })
                    else -> emptyList()
                }
                JSONObject().put("raw_title", original.optString("title")).put("kind", "agent_prompt").put("locked", true).put("notify", original.optBoolean("notify", true))
                    .put("location", (listOf(source.name) + place).joinToString("｜")).put("options", JSONArray())
            } else JSONObject(original.toString()).also { it.put("location", source.name + "｜" + it.optString("location")) }
            item.put("id", DecisionState.key(source.id, id)).put("source", source.id)
            items.put(item)
        }
        val previous = DecisionState.decisions
        val old = (0 until previous.length()).mapNotNull { previous.optJSONObject(it) }.associate { it.optString("id") to it.optBoolean("notify") }
        val currentKeys = (0 until items.length()).map { items.getJSONObject(it).getString("id") }.toSet()
        val sourcePrefix = DecisionState.key(source.id, "")
        (old.keys.filter { it.startsWith(sourcePrefix) } - currentKeys).forEach { key -> posted.remove(key); notificationIds.remove(key)?.let(manager::cancel) }
        // A decision that is gone may ring again only if it comes back as a new one.
        alerted.removeAll { it.startsWith(sourcePrefix) && it !in currentKeys }
        val before = source.decisions
        source.decisions = items
        DecisionState.recompute()
        val all = DecisionState.decisions
        val activeVideos = (0 until all.length()).flatMap { index ->
            val videos = all.optJSONObject(index)?.optJSONArray("videos") ?: JSONArray()
            (0 until videos.length()).mapNotNull { videos.optJSONObject(it)?.optString("key") }
        }.toSet()
        (DecisionState.videos.keys - activeVideos).forEach { key -> DecisionState.videos.remove(key)?.delete() }
        (receiving.keys - activeVideos).forEach { key -> receiving.remove(key)?.first?.close(); File(cacheDir, "video-$key.partial").delete() }
        for (index in 0 until items.length()) {
            val item = items.optJSONObject(index) ?: continue
            val key = item.optString("id")
            val action = notifyAction(item.optBoolean("notify"), old[key] == true, key in alerted)
            if (action == NotifyAction.CANCEL) { notificationIds[key]?.let(manager::cancel); posted.remove(key); continue }
            if (action == NotifyAction.KEEP) {
                // The choices arrived (unlocked) or went away (locked): update the shown notification without ringing.
                if (posted[key] != notificationContent(item, source.operable)) postDecision(key, item, source.operable, quietUpdate = true)
                continue
            }
            alerted.add(key)
            // Shown again after the PC stopped showing it: the quiet channel has no sound and no pop-up over what the user is typing.
            postDecision(key, item, source.operable, quiet = action == NotifyAction.SILENT)
        }
        // 通知を出している判断待ちを、出どころの PC に知らせる。つなぎ直した一覧でも同じ分をもう一度送る。
        DecisionState.shown(notifiedIds((0 until items.length()).mapNotNull { items.optJSONObject(it) }
            .map { it.optString("id") to it.optBoolean("notify") }, manager.areNotificationsEnabled()))
        // Only the notify flags moved (the PC's focus or space changed): the screens stay as they are,
        // so a field being typed in keeps its focus and the open decision stays open.
        if (connectedBefore && sameForDisplay(jsonItems(before), jsonItems(items))) {
            manager.notify(FOREGROUND_ID, connectionNotification())
            return
        }
        changed()
    }
    private fun jsonItems(array: JSONArray): List<JSONObject> = (0 until array.length()).mapNotNull { array.optJSONObject(it) }

    /**
     * Shows one decision's notification. While the phone is unlocked for its PC, each choice is a
     * button, which a paired Pixel Watch shows too; otherwise tapping opens the app to unlock.
     * quiet: the silent channel. quietUpdate: same channel, but an update that does not ring again.
     */
    private fun postDecision(key: String, item: JSONObject, operable: Boolean, quiet: Boolean = false, quietUpdate: Boolean = false, status: String? = null, buttons: Boolean = true) {
        val number = notificationIds.getOrPut(key) { nextNotification++ }
        val choices = notificationChoices(item, operable)
        posted[key] = notificationContent(item, operable)
        val location = item.optString("location", "判断を待っています")
        val builder = Notification.Builder(this, if (quiet) QUESTIONS_QUIET else QUESTIONS).setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(decisionHeading(item.optString("raw_title"), item.optString("raw_body"), item.optString("kind")))
            .setContentText(status ?: location)
            .setColor(0xFF33FF66.toInt())
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .setStyle(Notification.BigTextStyle().bigText(if (status == null) location else "$status\n$location"))
            .setOnlyAlertOnce(quietUpdate)
            .setContentIntent(panel(number, Intent(this, MainActivity::class.java).putExtra("decision", key))).setAutoCancel(true)
        if (choices != null && buttons) {
            val actions = choices.mapIndexed { index, (option, label) ->
                // The data names the decision and the choice, so every button is its own PendingIntent:
                // a later button can never redirect an earlier one to another decision or choice.
                val answer = Intent(this, DecisionService::class.java).setAction(ANSWER).setData(answerUri(key, option))
                    .putExtra("decision", key).putExtra("option", option)
                Notification.Action.Builder(null, label, PendingIntent.getService(this, index, answer,
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)).build()
            }
            // The phone shows up to three buttons; the watch lists every choice.
            actions.take(3).forEach(builder::addAction)
            builder.extend(Notification.WearableExtender().addActions(actions))
        }
        manager.notify(number, builder.build())
    }

    /** A button on the notification (or the watch) was pressed: answer with that one choice. */
    private fun answerFromNotification(key: String, option: String) {
        val item = jsonItems(DecisionState.decisions).firstOrNull { it.optString("id") == key } ?: return
        val source = DecisionState.split(key)?.let { DecisionState.sources[it.first] }
        if (source == null || !DecisionState.reachable(key) || notificationChoices(item, source.operable)?.any { it.first == option } != true) {
            postDecision(key, item, source?.operable == true, quietUpdate = true, status = "スマホで顔・指紋で開いてから答えてください", buttons = false)
            return
        }
        val sequence = java.util.UUID.randomUUID().toString()
        if (!DecisionState.answer(key, listOf(option), "", emptyList(), sequence)) {
            postDecision(key, item, source.operable, quietUpdate = true, status = "送れませんでした。もう一度押してください")
            return
        }
        notificationAnswers[sequence] = key
        postDecision(key, item, source.operable, quietUpdate = true, status = "送信中…", buttons = false)
    }

    /** The PC's answer to a notification button: gone when answered, otherwise the reason on the notification. */
    private fun notificationAnswerResult(message: JSONObject) {
        val key = notificationAnswers.remove(message.optString("sequence")) ?: return
        val status = message.optString("status")
        if (status == "answered" || status == "answered_elsewhere" || status == "cancelled" || status == "expired") {
            notificationIds[key]?.let(manager::cancel); posted.remove(key); return
        }
        val item = jsonItems(DecisionState.decisions).firstOrNull { it.optString("id") == key } ?: return
        val operable = DecisionState.split(key)?.let { DecisionState.sources[it.first]?.operable } == true
        postDecision(key, item, operable, quietUpdate = true, status = message.optString("message", "回答できませんでした"))
    }

    /** Unlocking or locking a PC adds or removes the choice buttons of its shown notifications, without ringing. */
    private fun refreshChoices(source: DecisionSource) {
        for (item in jsonItems(source.decisions)) {
            val key = item.optString("id")
            if (key in posted && posted[key] != notificationContent(item, source.operable)) postDecision(key, item, source.operable, quietUpdate = true)
        }
    }

    private fun receiveVideo(source: DecisionSource, message: JSONObject) {
        val key = message.optString("key")
        val id = DecisionState.key(source.id, message.optString("id"))
        val allowed = (0 until source.decisions.length()).mapNotNull { source.decisions.optJSONObject(it) }
            .any { item -> item.optString("id") == id && (item.optJSONArray("videos") ?: JSONArray()).let { videos ->
                (0 until videos.length()).any { videos.optJSONObject(it)?.optString("key") == key }
            } }
        if (!allowed || !key.matches(Regex("[0-9a-f-]{36}"))) return
        try {
            val index = message.getInt("index")
            val state = receiving[key] ?: if (index == 0) FileOutputStream(File(cacheDir, "video-$key.partial")) to 0 else return
            if (index != state.second) { state.first.close(); receiving.remove(key); return }
            state.first.write(Base64.decode(message.getString("data"), Base64.NO_WRAP))
            receiving[key] = state.first to index + 1
        } catch (_: Exception) { receiving.remove(key)?.first?.close(); File(cacheDir, "video-$key.partial").delete() }
    }
    private fun finishVideo(message: JSONObject) {
        val key = message.optString("key")
        val state = receiving.remove(key) ?: return
        state.first.close()
        val file = File(cacheDir, "video-$key")
        if (!File(cacheDir, "video-$key.partial").renameTo(file)) return
        DecisionState.videos[key] = file
        changed()
    }
    override fun onDestroy() {
        generation++
        connectivity.unregisterNetworkCallback(networkCallback)
        handler.removeCallbacksAndMessages(null)
        receiving.forEach { (key, value) -> value.first.close(); File(cacheDir, "video-$key.partial").delete() }
        receiving.clear()
        DecisionState.sources.values.forEach { it.link?.close("終了") }
        DecisionState.sources.clear()
        DecisionState.recompute()
        // With the service gone nothing keeps the notifications current: none may keep offering answers.
        notificationIds.values.forEach(manager::cancel)
        posted.clear()
        super.onDestroy()
    }
    override fun onBind(intent: Intent?): IBinder? = null
}

private fun answerUri(decision: String, option: String): android.net.Uri = android.net.Uri.parse(answerData(decision, option))
