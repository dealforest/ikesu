package kazuph.ikesu.decisions

import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.json.JSONObject
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit

/** One WebSocket through the relay to a bridge, carrying a Noise IK session (PROTOCOL 2–3).
 *  Callbacks run on OkHttp's thread; the owner moves them to the main thread. */
class RelayLink(
    private val url: String,
    private val tag: String,
    private val session: NoiseSession,
    private val listener: Listener,
) {
    interface Listener {
        fun onReady(link: RelayLink)
        fun onMessage(link: RelayLink, message: JSONObject)
        fun onClosed(link: RelayLink, reason: String)
    }

    @Volatile private var socket: WebSocket? = null
    @Volatile var closed = false; private set
    /** The relay closed us because the bridge was away or restarting (1001, 1012, 1013): retry soon, it may be back. */
    @Volatile var bridgeAway = false; private set
    val handshakeHash: ByteArray get() = session.handshakeHash
    val ready: Boolean get() = session.ready && !closed
    private val lock = Any()
    /** One thread seals and sends in order (Noise nonces must arrive in order) and paces large messages. */
    private val sender = Executors.newSingleThreadExecutor()

    fun start() {
        val request = Request.Builder().url(url).header("Authorization", "IKESU-RELAY $tag").build()
        socket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                synchronized(lock) { webSocket.send(session.first().toByteString()) }
            }
            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                try {
                    synchronized(lock) {
                        if (!session.ready) { session.finish(bytes.toByteArray()); listener.onReady(this@RelayLink); return }
                        val text = session.open(bytes.toByteArray()) ?: return
                        listener.onMessage(this@RelayLink, JSONObject(text))
                    }
                } catch (error: Exception) {
                    // A message that fails to decrypt or parse ends the session (PROTOCOL 3).
                    close("通信を読めませんでした: ${error.javaClass.simpleName}")
                }
            }
            override fun onMessage(webSocket: WebSocket, text: String) { close("中継から文字の1通が届きました") }
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                finish(if (response?.code == 401) "中継に入れませんでした（PC との登録を確かめてください）" else "中継につながりません: ${t.message ?: t.javaClass.simpleName}")
            }
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                bridgeAway = code == 1001 || code == 1012 || code == 1013
                finish("接続が閉じました（$code）")
            }
        })
    }

    /** Queues one message. Large ones (answers with photos) go out at about 3 MB a second, under the relay's
     *  4 MB-a-second limit per app, and never pile more than 1 MB into OkHttp's queue. */
    fun send(message: JSONObject): Boolean {
        val target = socket ?: return false
        if (!ready) return false
        val text = message.toString()
        return try {
            sender.execute {
                try {
                    val parts = synchronized(lock) { session.seal(text) }
                    for (part in parts) {
                        while (!closed && target.queueSize() > QUEUE_LIMIT) Thread.sleep(10)
                        if (closed || !target.send(part.toByteString())) return@execute
                        if (parts.size > 1) Thread.sleep(part.size * 1000L / PACE_BYTES_PER_SECOND)
                    }
                } catch (_: InterruptedException) {
                } catch (_: Exception) { close("つなぎ直しが必要です") }
            }
            true
        } catch (_: RejectedExecutionException) { false }
    }

    fun close(reason: String) {
        socket?.cancel()
        finish(reason)
    }

    private fun finish(reason: String) {
        if (closed) return
        closed = true
        sender.shutdownNow()
        listener.onClosed(this, reason)
    }

    companion object {
        private const val PACE_BYTES_PER_SECOND = 3_000_000L
        private const val QUEUE_LIMIT = 1024L * 1024L
        val client: OkHttpClient = OkHttpClient.Builder().pingInterval(20, TimeUnit.SECONDS)
            .connectTimeout(15, TimeUnit.SECONDS).followRedirects(false).followSslRedirects(false).build()

        fun appUrl(machine: Machine) = "${machine.relay.trimEnd('/')}/v1/app/${machine.room}/${machine.device}"
        fun pairUrl(code: PairingCode) = "${code.relay.trimEnd('/')}/v1/pair/${code.room}/${code.pairing}/${code.exp}"
    }
}
