package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Test

class ScreenRouteTest {
    // Switching from the Mac Air ("air") to another PC ("pc"): the open sent to air before the switch must not
    // leave the screen waiting, and air's screen must not stay open.
    @Test fun openedOnThePcSwitchedAwayFromIsClosed() {
        assertEquals(ScreenRoute.CLOSE_STRAY, screenRoute("air", "screen_opened", shown = "pc", openingFrom = "air"))
        assertEquals(ScreenRoute.CLOSE_STRAY, screenRoute("air", "screen_opened", shown = "pc", openingFrom = null))
    }

    @Test fun openedOnTheShownPcIsShownOnlyWhenItWasAskedFor() {
        assertEquals(ScreenRoute.HANDLE, screenRoute("pc", "screen_opened", shown = "pc", openingFrom = "pc"))
        // A late answer to an open that was dropped (the screen was left meanwhile) is closed, not shown.
        assertEquals(ScreenRoute.CLOSE_STRAY, screenRoute("pc", "screen_opened", shown = "pc", openingFrom = null))
    }

    @Test fun anErrorFromThePcStillOpeningFreesTheNextOpen() {
        assertEquals(ScreenRoute.RELEASE, screenRoute("air", "error", shown = "pc", openingFrom = "air"))
        assertEquals(ScreenRoute.IGNORE, screenRoute("air", "error", shown = "pc", openingFrom = "pc"))
    }

    @Test fun otherMessagesFollowTheShownPc() {
        assertEquals(ScreenRoute.HANDLE, screenRoute("pc", "screen_data", shown = "pc", openingFrom = null))
        assertEquals(ScreenRoute.IGNORE, screenRoute("air", "screen_data", shown = "pc", openingFrom = null))
        assertEquals(ScreenRoute.IGNORE, screenRoute("air", "operate_expired", shown = "pc", openingFrom = null))
    }
}
