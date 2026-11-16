package kazuph.ikesu.decisions

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import com.southernstorm.noise.protocol.Noise
import org.json.JSONArray
import org.json.JSONObject
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** One PC paired through the relay (PROTOCOL 3.2). */
data class Machine(
    val id: String, val name: String, val relay: String, val room: String, val bridge: String,
    val device: String, val tag: String, val permissions: List<String>,
) {
    fun toJson(): JSONObject = JSONObject().put("id", id).put("name", name).put("relay", relay).put("room", room)
        .put("bridge", bridge).put("device", device).put("tag", tag).put("permissions", JSONArray(permissions))

    companion object {
        fun fromJson(value: JSONObject) = Machine(value.getString("id"), value.getString("name"), value.getString("relay"),
            value.getString("room"), value.getString("bridge"), value.getString("device"), value.getString("tag"),
            value.optJSONArray("permissions")?.let { list -> (0 until list.length()).map { list.getString(it) } } ?: emptyList())
    }
}

/** The pairing QR (PROTOCOL 3.2 step 2), checked before anything is sent. */
data class PairingCode(val relay: String, val room: String, val bridge: String, val pairing: String, val secret: String, val exp: Long, val tag: String, val name: String) {
    companion object {
        fun parse(text: String, nowSeconds: Long): PairingCode {
            val json = JSONObject(text)
            require(json.optInt("v") == 1) { "この QR は ikesu のペアリング用ではありません" }
            val code = PairingCode(json.getString("relay"), json.getString("room"), json.getString("bridge"), json.getString("pairing"),
                json.getString("secret"), json.getLong("exp"), json.getString("tag"), json.getString("name"))
            val relay = java.net.URI(code.relay)
            require(relay.scheme == "wss" && !relay.host.isNullOrEmpty() && relay.userInfo == null && relay.query == null) { "中継の URL は wss:// で始まる必要があります" }
            require(B64.decode(code.room).size == 32 && B64.decode(code.bridge).size == 32 && B64.decode(code.pairing).size == 16 && B64.decode(code.secret).size == 32) { "QR の中身の長さが合いません" }
            require(code.tag.matches(Regex("[0-9a-f]{64}"))) { "QR のタグが不正です" }
            require(code.exp > nowSeconds) { "この QR は期限が切れています。PC でもう一度出してください" }
            return code
        }
    }
}

/** Connection details printed by `main.mjs devices add` on the PC, pasted into the app. */
object ManualLink {
    fun parse(text: String): Machine {
        val json = JSONObject(text.trim())
        require(json.optInt("v") == 1 && json.optString("kind") == "ikesu-link") { "ikesu の接続情報ではありません" }
        val relay = java.net.URI(json.getString("relay"))
        require(relay.scheme == "wss" && !relay.host.isNullOrEmpty() && relay.userInfo == null && relay.query == null) { "中継の URL は wss:// で始まる必要があります" }
        val machine = Machine(java.util.UUID.randomUUID().toString(), json.getString("name"), json.getString("relay"), json.getString("room"),
            json.getString("bridge"), json.getString("device"), json.getString("tag"), listOf("notify", "answer", "screen", "input"))
        require(B64.decode(machine.room).size == 32 && B64.decode(machine.bridge).size == 32 && B64.decode(machine.device).size == 16) { "接続情報の中身の長さが合いません" }
        require(machine.tag.matches(Regex("[0-9a-f]{64}"))) { "接続情報のタグが不正です" }
        return machine
    }

    /** The public keys to register on a PC, as one line to send to the PC's owner. */
    fun publicKeys(store: MachineStore, name: String): String = JSONObject().put("v", 1).put("kind", "ikesu-device").put("name", name)
        .put("noise", B64.encode(store.noisePublicKey())).put("operate", B64.encode(MachineStore.operatePublicKey())).toString()
}

class MachineStore(context: Context) {
    private val preferences = context.getSharedPreferences("machines", Context.MODE_PRIVATE)

    fun list(): List<Machine> = runCatching {
        val array = JSONArray(preferences.getString("list", "[]"))
        (0 until array.length()).map { Machine.fromJson(array.getJSONObject(it)) }
    }.getOrDefault(emptyList())

    fun add(machine: Machine) = save(list().filter { it.id != machine.id && !(it.room == machine.room && it.bridge == machine.bridge) } + machine)
    fun remove(id: String) = save(list().filter { it.id != id })
    private fun save(machines: List<Machine>) { preferences.edit().putString("list", JSONArray(machines.map { it.toJson() }).toString()).apply() }

    /** The app's Noise static key, created once and wrapped by a Keystore key that needs no user authentication
     *  so the background service can keep receiving notifications. */
    fun noisePrivateKey(): ByteArray {
        val wrapped = preferences.getString("noise", null)
        if (wrapped != null) return unwrap(wrapped)
        val dh = Noise.createDH("25519")
        dh.generateKeyPair()
        val private = ByteArray(32).also { dh.getPrivateKey(it, 0) }
        preferences.edit().putString("noise", wrap(private)).apply()
        return private
    }

    fun noisePublicKey(): ByteArray {
        val dh = Noise.createDH("25519")
        dh.setPrivateKey(noisePrivateKey(), 0)
        return ByteArray(32).also { dh.getPublicKey(it, 0) }
    }

    private fun wrappingKey(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(WRAP_ALIAS, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(WRAP_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }

    private fun wrap(secret: ByteArray): String {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, wrappingKey()) }
        val sealed = cipher.doFinal(secret)
        return Base64.encodeToString(cipher.iv, Base64.NO_WRAP) + ":" + Base64.encodeToString(sealed, Base64.NO_WRAP)
    }

    private fun unwrap(value: String): ByteArray {
        val (iv, sealed) = value.split(":")
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, wrappingKey(), GCMParameterSpec(128, Base64.decode(iv, Base64.NO_WRAP)))
        return cipher.doFinal(Base64.decode(sealed, Base64.NO_WRAP))
    }

    companion object {
        private const val WRAP_ALIAS = "ikesu-relay-noise"
        const val OPERATE_ALIAS = "ikesu-operate"

        /** The operate key: ECDSA P-256 inside the Keystore, usable for one signature per strong biometric
         *  authentication, invalidated when a new fingerprint or face is enrolled (PROTOCOL 1). */
        fun operatePublicKey(): ByteArray {
            val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            store.getCertificate(OPERATE_ALIAS)?.let { return it.publicKey.encoded }
            val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
            generator.initialize(KeyGenParameterSpec.Builder(OPERATE_ALIAS, KeyProperties.PURPOSE_SIGN)
                .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
                .setDigests(KeyProperties.DIGEST_SHA256)
                .setUserAuthenticationRequired(true)
                .setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                .setInvalidatedByBiometricEnrollment(true)
                .build())
            return generator.generateKeyPair().public.encoded
        }

        /** A Signature ready for BiometricPrompt, or null when the key was invalidated (re-pairing needed). */
        fun operateSignature(): Signature? = try {
            val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
            val key = store.getKey(OPERATE_ALIAS, null) as? PrivateKey ?: return null
            Signature.getInstance("SHA256withECDSA").apply { initSign(key) }
        } catch (_: android.security.keystore.KeyPermanentlyInvalidatedException) { null }

        fun forgetOperateKey() {
            KeyStore.getInstance("AndroidKeyStore").apply { load(null) }.deleteEntry(OPERATE_ALIAS)
        }
    }
}
