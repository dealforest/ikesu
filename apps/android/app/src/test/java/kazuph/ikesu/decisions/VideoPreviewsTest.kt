package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Test

class VideoPreviewsTest {
    @Test fun lengthIsMinutesAndSeconds() {
        assertEquals("", VideoPreviews.length(0))
        assertEquals("0:04", VideoPreviews.length(4000))
        assertEquals("0:05", VideoPreviews.length(4600))
        assertEquals("12:30", VideoPreviews.length(750_000))
    }
}
