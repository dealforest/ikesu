package kazuph.ikesu.decisions

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.hardware.biometrics.BiometricManager
import android.hardware.biometrics.BiometricPrompt
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import org.json.JSONObject
import java.util.UUID

/** Adds a PC through its pairing QR (PROTOCOL 3.2): checks the QR, shows the PC name and key
 *  fingerprint, proves the pairing secret inside the Noise session, shows the six digits the user
 *  types on the PC, and stores the PC when the bridge confirms. */
class Pairing(private val activity: Activity, private val done: () -> Unit) {
    private val handler = Handler(Looper.getMainLooper())
    private var link: RelayLink? = null
    private var dialog: AlertDialog? = null

    fun scanned(text: String) {
        val code = try { PairingCode.parse(text, System.currentTimeMillis() / 1000) }
        catch (error: Exception) { toast(error.message ?: "QR を読めませんでした"); return }
        AlertDialog.Builder(activity).setTitle("この PC を登録しますか？")
            .setMessage("PC の名前: ${code.name}\n鍵の指紋: ${RelayMath.fingerprint(B64.decode(code.bridge))}\n\nPC の端末に出ている名前と指紋が同じか確かめてください。")
            .setPositiveButton("登録する") { _, _ -> start(code) }
            .setNegativeButton("やめる", null).show()
    }

    private fun start(code: PairingCode) {
        val store = MachineStore(activity)
        val operate = try { MachineStore.operatePublicKey() } catch (error: Exception) {
            toast("顔・指紋の鍵を作れませんでした。端末に指紋か顔を登録してください（${error.javaClass.simpleName}）"); return
        }
        val secret = B64.decode(code.secret)
        val session = NoiseSession(store.noisePrivateKey(), B64.decode(code.bridge), B64.decode(code.room))
        showDialog("PC につないでいます…")
        val next = RelayLink(RelayLink.pairUrl(code), code.tag, session, object : RelayLink.Listener {
            override fun onReady(link: RelayLink) {
                val hash = link.handshakeHash
                link.send(JSONObject().put("type", "pair").put("proof", B64.encode(RelayMath.pairProof(secret, hash)))
                    .put("name", android.os.Build.MODEL).put("operate", B64.encode(operate)))
                val digits = RelayMath.sas(secret, hash)
                handler.post { showDialog("PC の端末に、次の6桁を打ってください。\n\n${digits.substring(0, 3)} ${digits.substring(3)}\n\n打つと登録が終わります。") }
            }
            override fun onMessage(link: RelayLink, message: JSONObject) {
                if (message.optString("type") != "paired") return
                val machine = try {
                    Machine(UUID.randomUUID().toString(), message.optString("name", code.name), code.relay, code.room, code.bridge,
                        message.getString("device").also { require(B64.decode(it).size == 16) },
                        message.getString("tag").also { require(it.matches(Regex("[0-9a-f]{64}"))) },
                        message.optJSONArray("permissions")?.let { list -> (0 until list.length()).map { list.getString(it) } } ?: emptyList())
                } catch (_: Exception) { link.close("登録の返事が不正です"); return }
                store.add(machine)
                link.close("登録が終わりました")
                handler.post {
                    dialog?.dismiss(); dialog = null
                    toast("${machine.name} を登録しました")
                    activity.startService(Intent(activity, DecisionService::class.java).setAction(DecisionService.RECONNECT))
                    done()
                }
            }
            override fun onClosed(link: RelayLink, reason: String) {
                handler.post {
                    if (this@Pairing.link !== link) return@post
                    this@Pairing.link = null
                    if (dialog != null) { dialog?.dismiss(); dialog = null; if (reason != "登録が終わりました") toast("登録できませんでした: $reason") }
                }
            }
        })
        link = next
        next.start()
    }

    private fun showDialog(message: String) {
        dialog?.dismiss()
        dialog = AlertDialog.Builder(activity).setTitle("PC の登録").setMessage(message).setCancelable(false)
            .setNegativeButton("やめる") { _, _ -> dialog = null; link?.close("やめました") }.show()
    }

    private fun toast(message: String) = Toast.makeText(activity, message, Toast.LENGTH_LONG).show()
}

/** Unlocks operation on every connected PC with one strong biometric authentication (PROTOCOL 3.3). */
object Unlock {
    @Volatile private var prompting = false

    fun run(activity: Activity, onFailure: (String) -> Unit) {
        val targets = DecisionState.lockedLinks()
        if (targets.isEmpty() || prompting) return
        val status = activity.getSystemService(BiometricManager::class.java).canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG)
        if (status != BiometricManager.BIOMETRIC_SUCCESS) { onFailure("この端末では強い生体認証（指紋など）が使えません"); return }
        val signature = MachineStore.operateSignature()
        if (signature == null) {
            // The invalidated key must go, or the next pairing would register the same dead public key again.
            try { MachineStore.forgetOperateKey() } catch (_: Exception) { }
            onFailure("指紋や顔の登録が変わったため、操作の鍵が使えなくなりました。「鍵を作る」で新しい鍵を作り、各 PC に登録し直してください")
            return
        }
        val hashes = targets.mapNotNull { it.link?.handshakeHash?.copyOf() }.take(8)
        val executor = activity.mainExecutor
        prompting = true
        BiometricPrompt.Builder(activity).setTitle("ikesu を開く").setSubtitle("顔・指紋で、判断待ちの中身と操作を開きます")
            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
            .setNegativeButton("やめる", executor) { _, _ -> prompting = false }
            .build()
            .authenticate(BiometricPrompt.CryptoObject(signature), CancellationSignal(), executor, object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    prompting = false
                    try {
                        val authorized = result.cryptoObject?.signature ?: error("署名を取り出せません")
                        authorized.update(RelayMath.operateMessage(hashes))
                        DecisionState.operate(targets, hashes, authorized.sign())
                    } catch (error: Exception) { onFailure("署名できませんでした: ${error.javaClass.simpleName}") }
                }
                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) { prompting = false }
            })
    }
}
