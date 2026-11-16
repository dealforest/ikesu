package kazuph.ikesu.decisions

import com.southernstorm.noise.protocol.CipherState
import com.southernstorm.noise.protocol.HandshakeState
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.BufferedReader
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.io.InputStreamReader
import java.net.Socket

/** The app (noise-java) and the bridge (noise-handshake) must agree on
 *  Noise_IK_25519_ChaChaPoly_BLAKE2b: the same handshake hash, working
 *  transport keys, and refusal of a tampered message. */
class NoiseInteropTest {
    private fun hex(bytes: ByteArray) = bytes.joinToString("") { "%02x".format(it) }
    private fun unhex(text: String) = ByteArray(text.length / 2) { text.substring(it * 2, it * 2 + 2).toInt(16).toByte() }

    @Test fun ikHandshakeAndTransportMatchTheBridge() {
        val bridge = File("../bridge").absoluteFile
        val node = ProcessBuilder("node", "test/noise-interop-peer.mjs", "ikesu-mobile/1").directory(bridge).redirectErrorStream(true).start()
        try {
            val out = BufferedReader(InputStreamReader(node.inputStream))
            val first = out.readLine().split(" ")
            val port = first[1].toInt(); val bridgeKey = unhex(first[3])
            val hs = HandshakeState("Noise_IK_25519_ChaChaPoly_BLAKE2b", HandshakeState.INITIATOR)
            hs.setPrologue("ikesu-mobile/1".toByteArray(), 0, 14)
            hs.localKeyPair.generateKeyPair()
            hs.remotePublicKey.setPublicKey(bridgeKey, 0)
            hs.start()
            Socket("127.0.0.1", port).use { socket ->
                val input = DataInputStream(socket.getInputStream()); val output = DataOutputStream(socket.getOutputStream())
                fun write(data: ByteArray, length: Int) { output.writeShort(length); output.write(data, 0, length); output.flush() }
                fun read(): ByteArray { val n = input.readUnsignedShort(); return ByteArray(n).also { input.readFully(it) } }
                val buffer = ByteArray(65535)
                write(buffer, hs.writeMessage(buffer, 0, ByteArray(0), 0, 0))
                val reply = read()
                hs.readMessage(reply, 0, reply.size, buffer, 0)
                assertEquals(HandshakeState.SPLIT, hs.action)
                val hash = hs.handshakeHash
                val pair = hs.split()
                val send: CipherState = pair.sender; val receive: CipherState = pair.receiver
                val line = out.readLine().split(" ")
                assertEquals("HANDSHAKE", line[0])
                assertEquals(hex(hash), line[1])
                val myKey = ByteArray(32).also { hs.localKeyPair.getPublicKey(it, 0) }
                assertEquals(hex(myKey), line[3])
                for (text in listOf("hello", "ペインの画面")) {
                    val plain = text.toByteArray()
                    val n = send.encryptWithAd(null, plain, 0, buffer, 0, plain.size)
                    write(buffer, n)
                    val back = read()
                    val m = receive.decryptWithAd(null, back, 0, buffer, 0, back.size)
                    assertArrayEquals(plain.reversedArray(), buffer.copyOf(m))
                }
                // A flipped bit is refused and the bridge drops the connection.
                val plain = "tampered".toByteArray()
                val n = send.encryptWithAd(null, plain, 0, buffer, 0, plain.size)
                buffer[3] = (buffer[3].toInt() xor 1).toByte()
                write(buffer, n)
                assertTrue(out.readLine().startsWith("REJECT"))
            }
        } finally { node.destroy() }
    }
}
