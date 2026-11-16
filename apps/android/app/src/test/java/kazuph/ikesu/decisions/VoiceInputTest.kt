package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class VoiceInputTest {
    @Test fun endpointAcceptsBaseOrFullPath() {
        assertEquals("http://ubuntu-3090:8002/v1/audio/transcriptions", SpeechSettings.endpoint("http://ubuntu-3090:8002/").getOrThrow())
        assertEquals("https://stt.example.ts.net/v1/audio/transcriptions", SpeechSettings.endpoint("https://stt.example.ts.net/v1/audio/transcriptions").getOrThrow())
    }

    @Test fun endpointRefusesOtherSchemesAndSecrets() {
        assertTrue(SpeechSettings.endpoint("").isFailure)
        assertTrue(SpeechSettings.endpoint("ws://ubuntu-3090:8002").isFailure)
        assertTrue(SpeechSettings.endpoint("http://user:pass@ubuntu-3090:8002").isFailure)
        assertTrue(SpeechSettings.endpoint("http://ubuntu-3090:8002/?key=1").isFailure)
    }

    @Test fun appendKeepsTypedTextOnItsOwnLine() {
        assertEquals("進めて", Transcriber.append("", "進めて"))
        assertEquals("メモ\n進めて", Transcriber.append("メモ", "進めて"))
        assertEquals("メモ\n進めて", Transcriber.append("メモ\n", "進めて"))
        assertEquals("メモ", Transcriber.append("メモ", ""))
    }
}
