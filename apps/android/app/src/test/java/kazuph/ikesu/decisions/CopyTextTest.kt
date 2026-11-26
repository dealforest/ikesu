package kazuph.ikesu.decisions

import org.junit.Assert.assertEquals
import org.junit.Test

class CopyTextTest {
    @Test fun japaneseSentenceWrappedByThePaneComesBackAsOneLine() {
        // 幅 24 マス、字下げ 2 のペインに、AI が全角の文を折り返して描いた形。
        val rows = listOf(
            "  同じ 4 vCPU VM 1台（並",
            "  列分散なし）での pass を",
            "  短くしました。",
        )
        assertEquals("同じ 4 vCPU VM 1台（並列分散なし）での pass を短くしました。", CopyText.unwrap(rows, 24))
    }

    @Test fun latinWordsWrappedAtASpaceGetTheSpaceBack() {
        val rows = listOf(
            "    run `npm run",
            "    pass` again",
        )
        // "pass`" (5) would not fit after "    run `npm run" (16) in 20 columns: it was wrapped.
        assertEquals("run `npm run pass` again", CopyText.unwrap(rows, 20))
    }

    @Test fun shortLinesBlankLinesAndListItemsKeepTheirBreaks() {
        val rows = listOf(
            "  ▼ 次やること",
            "  1. 最終の確認をして、ui-live へ統合",
            "     する",
            "  2. 手元の mini へ移す",
            "",
            "  おわり",
        )
        assertEquals("▼ 次やること\n1. 最終の確認をして、ui-live へ統合する\n2. 手元の mini へ移す\n\nおわり", CopyText.unwrap(rows, 36))
    }

    @Test fun aLineThatEndedEarlyIsNotJoinedToTheNext() {
        val rows = listOf("  短い行", "  次の行")
        assertEquals("短い行\n次の行", CopyText.unwrap(rows, 40))
    }

    @Test fun aDifferentIndentStartsANewLine() {
        val rows = listOf("  あいうえおかきくけこさしすせそ", "      コードの行")
        assertEquals("あいうえおかきくけこさしすせそ\n    コードの行", CopyText.unwrap(rows, 32))
    }
}
