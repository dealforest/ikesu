package kazuph.ikesu.decisions

import com.southernstorm.noise.protocol.CipherState
import com.southernstorm.noise.protocol.HandshakeState
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.security.MessageDigest
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec

/** Encoding helpers shared with the bridge (apps/android/PROTOCOL.md). */
object B64 {
    fun encode(bytes: ByteArray): String = java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    fun decode(text: String): ByteArray = java.util.Base64.getUrlDecoder().decode(text)
}

object RelayMath {
    const val PROLOGUE = "ikesu-mobile/1"
    const val OPERATE = "ikesu-operate/1"

    fun hmac(key: ByteArray, vararg parts: ByteArray): ByteArray = Mac.getInstance("HmacSHA256").run {
        init(SecretKeySpec(key, "HmacSHA256")); parts.forEach { update(it) }; doFinal()
    }

    /** The six digits both sides show and compare during pairing (PROTOCOL 3.2). */
    fun sas(secret: ByteArray, handshakeHash: ByteArray): String {
        val mac = hmac(secret, "sas".toByteArray(), handshakeHash)
        val number = ByteBuffer.wrap(mac, 0, 4).int.toLong() and 0xffffffffL
        return "%06d".format(number % 1_000_000)
    }

    fun pairProof(secret: ByteArray, handshakeHash: ByteArray): ByteArray = hmac(secret, "pair".toByteArray(), handshakeHash)

    /** First eight bytes of SHA-256 of the bridge key, shown before pairing. */
    fun fingerprint(publicKey: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(publicKey).copyOf(8).joinToString(":") { "%02x".format(it) }

    /** The text signed by the operate key: covers every connected PC at once. */
    fun operateMessage(hashes: List<ByteArray>): ByteArray {
        require(hashes.size in 1..8 && hashes.all { it.size == 64 })
        return ByteArrayOutputStream().apply { write(OPERATE.toByteArray()); write(hashes.size); hashes.forEach { write(it) } }.toByteArray()
    }
}

/** One Noise_IK initiator session with the bridge. Not thread-safe: callers serialize. */
class NoiseSession(private val localPrivate: ByteArray, private val bridgePublic: ByteArray, room: ByteArray) {
    private val handshake = HandshakeState("Noise_IK_25519_ChaChaPoly_BLAKE2b", HandshakeState.INITIATOR)
    private var sender: CipherState? = null
    private var receiver: CipherState? = null
    var handshakeHash: ByteArray = ByteArray(0); private set
    private var sent = 0L
    private val nextIds = java.util.concurrent.atomic.AtomicInteger(1)
    private val partial = mutableMapOf<Int, ByteArrayOutputStream>()

    init {
        val prologue = RelayMath.PROLOGUE.toByteArray() + room
        handshake.setPrologue(prologue, 0, prologue.size)
        handshake.localKeyPair.setPrivateKey(localPrivate, 0)
        handshake.remotePublicKey.setPublicKey(bridgePublic, 0)
        handshake.start()
    }

    fun first(): ByteArray {
        val buffer = ByteArray(256)
        val n = handshake.writeMessage(buffer, 0, ByteArray(0), 0, 0)
        return buffer.copyOf(n)
    }

    fun finish(reply: ByteArray) {
        val payload = ByteArray(256)
        handshake.readMessage(reply, 0, reply.size, payload, 0)
        check(handshake.action == HandshakeState.SPLIT) { "握手が終わりませんでした" }
        // A copy: destroy() wipes the handshake state's own array.
        handshakeHash = handshake.handshakeHash.copyOf()
        val pair = handshake.split()
        sender = pair.sender; receiver = pair.receiver
        handshake.destroy()
    }

    val ready: Boolean get() = sender != null

    /** Encrypts one JSON message, split into Noise messages of at most 60,000 plaintext bytes. */
    fun seal(json: String): List<ByteArray> {
        val body = json.toByteArray(Charsets.UTF_8)
        val plains = if (body.size + 1 <= MAX_PLAIN) listOf(byteArrayOf(0) + body) else {
            val id = nextIds.getAndIncrement()
            val step = MAX_PLAIN - 6
            (body.indices step step).map { start ->
                val end = minOf(body.size, start + step)
                ByteBuffer.allocate(6 + end - start).put(1).putInt(id).put(if (end == body.size) 1 else 0).put(body, start, end - start).array()
            }
        }
        val cipher = sender ?: error("握手の前です")
        return plains.map { plain ->
            check(++sent < (1L shl 31)) { "つなぎ直しが必要です" }
            val out = ByteArray(plain.size + 16)
            out.copyOf(cipher.encryptWithAd(null, plain, 0, out, 0, plain.size))
        }
    }

    /** Decrypts one Noise message; returns a whole JSON text when one is complete. Throws on tampering. */
    fun open(message: ByteArray): String? {
        val cipher = receiver ?: error("握手の前です")
        val plain = ByteArray(message.size)
        val n = cipher.decryptWithAd(null, message, 0, plain, 0, message.size)
        require(n >= 1) { "空の1通です" }
        return when (plain[0].toInt()) {
            0 -> String(plain, 1, n - 1, Charsets.UTF_8)
            1 -> {
                require(n >= 6) { "分割の形が不正です" }
                val buffer = ByteBuffer.wrap(plain, 1, 5)
                val flag = buffer.int.let { id -> id to buffer.get().toInt() }
                val id = flag.first
                require(flag.second == 0 || flag.second == 1) { "分割の印が不正です" }
                val last = flag.second == 1
                // A middle part is always full (PROTOCOL 3.1); empty or short parts are not collected.
                require(last || n - 6 == MAX_PLAIN - 6) { "分割の大きさが不正です" }
                require(id in partial || partial.size < 4) { "分割中のメッセージが多すぎます" }
                val stream = partial.getOrPut(id) { ByteArrayOutputStream() }
                stream.write(plain, 6, n - 6)
                require(partial.values.sumOf { it.size().toLong() } <= MAX_MESSAGE) { "メッセージが大きすぎます" }
                if (!last) null else String(partial.remove(id)!!.toByteArray(), Charsets.UTF_8)
            }
            else -> throw IllegalArgumentException("平文の形が不正です")
        }
    }

    companion object {
        const val MAX_PLAIN = 60_000
        const val MAX_MESSAGE = 128 * 1024 * 1024
    }
}
