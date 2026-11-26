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

    @Test fun aUrlCutByTheWidthJoinsWithoutASpace() {
        assertEquals("https://a.co/x", CopyText.unwrap(listOf("https://a.co", "/x"), 12))
        assertEquals("see https://example.com/a/b/c/d", CopyText.unwrap(listOf("see https://example", ".com/a/b/c/d"), 19))
    }

    @Test fun rowsInsideACodeFenceAndAUrlOverThreeRows() {
        val code = listOf("```", "val x = 1", "val y = 2", "val z = 3", "```", "あと")
        assertEquals(code.joinToString("\n"), CopyText.unwrap(code, 9))
        assertEquals("https://example.com/aaaa/bbbb/cc", CopyText.unwrap(listOf("https://exa", "mple.com/aa", "aa/bbbb/cc"), 11))
    }

    @Test fun tablesCodeFencesAndMarkdownMarkersKeepTheirBreaks() {
        val table = listOf("| a    | b    |", "| 1    | 2    |")
        assertEquals(table.joinToString("\n"), CopyText.unwrap(table, 15))
        val fence = listOf("```kotlin", "val x = 1")
        assertEquals(fence.joinToString("\n"), CopyText.unwrap(fence, 9))
        assertEquals("長い行がちょうどここで終わる\n+ 項目\n> 引用", CopyText.unwrap(listOf("長い行がちょうどここで終わる", "+ 項目", "> 引用"), 28))
    }

    @Test fun aDifferentIndentStartsANewLine() {
        val rows = listOf("  あいうえおかきくけこさしすせそ", "      コードの行")
        assertEquals("あいうえおかきくけこさしすせそ\n    コードの行", CopyText.unwrap(rows, 32))
    }
}
