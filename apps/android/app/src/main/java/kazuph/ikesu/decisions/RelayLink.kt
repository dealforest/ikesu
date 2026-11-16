package kazuph.ikesu.decisions

import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.json.JSONObject
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
    val handshakeHash: ByteArray get() = session.handshakeHash
    val ready: Boolean get() = session.ready && !closed
    private val lock = Any()

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
            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) { finish("接続が閉じました（$code）") }
        })
    }

    fun send(message: JSONObject): Boolean {
        val target = socket ?: return false
        if (!ready) return false
        return try {
            synchronized(lock) { session.seal(message.toString()).all { target.send(it.toByteString()) } }
        } catch (_: Exception) { close("つなぎ直しが必要です"); false }
    }

    fun close(reason: String) {
        socket?.cancel()
        finish(reason)
    }

    private fun finish(reason: String) {
        if (closed) return
        closed = true
        listener.onClosed(this, reason)
    }

    companion object {
        val client: OkHttpClient = OkHttpClient.Builder().pingInterval(20, TimeUnit.SECONDS)
            .connectTimeout(15, TimeUnit.SECONDS).followRedirects(false).followSslRedirects(false).build()

        fun appUrl(machine: Machine) = "${machine.relay.trimEnd('/')}/v1/app/${machine.room}/${machine.device}"
        fun pairUrl(code: PairingCode) = "${code.relay.trimEnd('/')}/v1/pair/${code.room}/${code.pairing}/${code.exp}"
    }
}
