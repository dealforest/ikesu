package kazuph.ikesu.decisions

import android.content.Context
import android.media.MediaRecorder
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.asRequestBody
import org.json.JSONObject
import java.io.File
import java.util.concurrent.TimeUnit

/** Where spoken answers are turned into text: an OpenAI compatible
 *  /v1/audio/transcriptions server the user runs (for example over Tailscale).
 *  Audio goes only to this address. */
class SpeechSettings(context: Context) {
    private val preferences = context.getSharedPreferences("speech", Context.MODE_PRIVATE)
    var url: String
        get() = preferences.getString("url", "") ?: ""
        set(value) { preferences.edit().putString("url", value).apply() }
    var model: String
        get() = preferences.getString("model", DEFAULT_MODEL) ?: DEFAULT_MODEL
        set(value) { preferences.edit().putString("model", value.ifBlank { DEFAULT_MODEL }).apply() }
    var language: String
        get() = preferences.getString("language", "ja") ?: "ja"
        set(value) { preferences.edit().putString("language", value.ifBlank { "ja" }).apply() }

    companion object {
        const val DEFAULT_MODEL = "whisper-large-v3-turbo"

        /** The transcription endpoint for a configured address, or an error message. */
        fun endpoint(url: String): Result<String> {
            val trimmed = url.trim().trimEnd('/')
            val target = runCatching { java.net.URI(trimmed) }.getOrNull()
            if (trimmed.isEmpty() || target == null || target.scheme !in listOf("http", "https") || target.host.isNullOrEmpty() ||
                target.userInfo != null || target.query != null || target.fragment != null) {
                return Result.failure(IllegalArgumentException("音声の文字起こしの接続先は http:// または https:// のアドレスを指定してください"))
            }
            return Result.success(if (trimmed.endsWith("/v1/audio/transcriptions")) trimmed else "$trimmed/v1/audio/transcriptions")
        }
    }
}

/** Records one spoken answer to an AAC file in the app's cache. */
class VoiceRecorder(private val context: Context) {
    private var recorder: MediaRecorder? = null
    private var file: File? = null
    val recording: Boolean get() = recorder != null

    fun start() {
        check(recorder == null)
        val output = File.createTempFile("voice", ".m4a", context.cacheDir)
        val next = MediaRecorder(context).apply {
            setAudioSource(MediaRecorder.AudioSource.VOICE_RECOGNITION)
            setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            setAudioChannels(1)
            setAudioSamplingRate(16_000)
            setAudioEncodingBitRate(64_000)
            setOutputFile(output.absolutePath)
        }
        try { next.prepare(); next.start() } catch (error: Exception) { next.release(); output.delete(); throw error }
        recorder = next
        file = output
    }

    /** Stops and returns the recording, or null when nothing usable was recorded. */
    fun stop(): File? {
        val current = recorder ?: return null
        val output = file
        recorder = null
        file = null
        val ok = runCatching { current.stop() }.isSuccess
        current.release()
        if (!ok || output == null || output.length() == 0L) { output?.delete(); return null }
        return output
    }

    fun cancel() { stop()?.delete() }
}

object Transcriber {
    private val client = OkHttpClient.Builder().callTimeout(90, TimeUnit.SECONDS).build()

    /** Sends the recording and returns the recognized text. Runs on a worker thread. */
    fun transcribe(settings: SpeechSettings, audio: File): String {
        val endpoint = SpeechSettings.endpoint(settings.url).getOrThrow()
        val body = MultipartBody.Builder().setType(MultipartBody.FORM)
            .addFormDataPart("file", "voice.m4a", audio.asRequestBody("audio/mp4".toMediaType()))
            .addFormDataPart("model", settings.model)
            .addFormDataPart("language", settings.language)
            .addFormDataPart("response_format", "json")
            .build()
        client.newCall(Request.Builder().url(endpoint).post(body).build()).execute().use { response ->
            val text = response.body?.string().orEmpty()
            if (!response.isSuccessful) throw IllegalStateException("文字起こしに失敗しました（HTTP ${response.code}）")
            return parse(text)
        }
    }

    fun parse(body: String): String {
        val text = runCatching { JSONObject(body).optString("text") }.getOrNull()
            ?: throw IllegalStateException("文字起こしの返事を読めませんでした")
        return text.trim()
    }

    /** Appends recognized text to what was already typed. */
    fun append(existing: String, recognized: String): String = when {
        recognized.isEmpty() -> existing
        existing.isBlank() -> recognized
        existing.endsWith("\n") || existing.endsWith(" ") -> existing + recognized
        else -> "$existing\n$recognized"
    }
}
