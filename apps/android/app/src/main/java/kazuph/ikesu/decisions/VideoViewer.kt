package kazuph.ikesu.decisions

import android.app.Dialog
import android.content.Context
import android.graphics.Bitmap
import android.graphics.Color
import android.media.MediaMetadataRetriever
import android.media.MediaPlayer
import android.view.Gravity
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.MediaController
import android.widget.TextView
import android.widget.VideoView
import java.io.File

/** The first frame and length of a received video, for the play button in the detail. */
data class VideoPreview(val frame: Bitmap?, val millis: Long)

object VideoPreviews {
    fun read(file: File): VideoPreview {
        val retriever = MediaMetadataRetriever()
        return try {
            retriever.setDataSource(file.absolutePath)
            val millis = retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull() ?: 0L
            // Scaled to the same 1600px bound as images, so a 4K video does not hold a full-size frame.
            VideoPreview(retriever.getScaledFrameAtTime(0, MediaMetadataRetriever.OPTION_CLOSEST_SYNC, 1600, 1600), millis)
        } catch (_: Exception) { VideoPreview(null, 0L) }
        finally { try { retriever.release() } catch (_: Exception) {} }
    }
    /** "0:04" / "12:30" style length shown on the play button. */
    fun length(millis: Long): String {
        if (millis <= 0) return ""
        val seconds = (millis + 500) / 1000
        return "%d:%02d".format(seconds / 60, seconds % 60)
    }
}

/**
 * Full-screen player above the activity. It starts at once, muted, and loops;
 * the sound button turns the sound on. Back or the close button closes it.
 * Volume keys are left to the system so the sound level can be changed while
 * a video plays (unlike the image viewer, which uses them for navigation).
 */
class VideoViewerDialog(context: Context, val key: String, file: File) : Dialog(context, android.R.style.Theme_Black_NoTitleBar_Fullscreen) {
    private var player: MediaPlayer? = null
    private val video: VideoView
    private val controller: MediaController
    private var muted = true
    init {
        val root = FrameLayout(context).apply { setBackgroundColor(Color.BLACK) }
        video = VideoView(context)
        root.addView(video, FrameLayout.LayoutParams(-1, -2, Gravity.CENTER))
        val density = context.resources.displayMetrics.density
        fun label(text: String, gravity: Int) = TextView(context).apply {
            this.text = text; textSize = 22f; setTextColor(0xFF33FF66.toInt())
            background = android.graphics.drawable.GradientDrawable().apply { setColor(0xCC000000.toInt()); setStroke((2 * density).toInt(), 0xFF33FF66.toInt()); cornerRadius = 8 * density }
            val pad = (16 * density).toInt(); setPadding(pad, pad, pad, pad)
            layoutParams = FrameLayout.LayoutParams(-2, -2, gravity).apply { val m = (24 * density).toInt(); setMargins(m, m * 2, m, m * 2) }
        }
        val close = label("閉じる", Gravity.TOP or Gravity.END).apply { setOnClickListener { dismiss() } }
        val sound = label("音を出す", Gravity.TOP or Gravity.START)
        sound.setOnClickListener {
            muted = !muted
            val volume = if (muted) 0f else 1f
            player?.setVolume(volume, volume)
            sound.text = if (muted) "音を出す" else "音を消す"
        }
        root.addView(close); root.addView(sound)
        setContentView(root, ViewGroup.LayoutParams(-1, -1))
        window?.setBackgroundDrawableResource(android.R.color.black)
        controller = MediaController(context)
        controller.setAnchorView(video)
        video.setMediaController(controller)
        video.setOnPreparedListener { mp -> player = mp; mp.isLooping = true; mp.setVolume(0f, 0f); video.start(); controller.show(3000) }
        var errorShown = false
        video.setOnErrorListener { _, _, _ ->
            if (!errorShown) { errorShown = true; root.addView(label("この動画はこの端末で再生できません", Gravity.CENTER)) }
            true
        }
        video.setVideoPath(file.absolutePath)
    }
    /** Runs on every dismiss; the activity's own dismiss listener does not replace it. */
    override fun onStop() { controller.hide(); video.stopPlayback(); player = null; super.onStop() }
}
