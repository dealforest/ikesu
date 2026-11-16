package screenprompt

import (
	"strings"
	"testing"
)

func TestWrappedQuestionText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lines   []string
		columns int
		want    string
	}{
		{"Japanese", []string{"かけ", "ら"}, 20, "かけら"},
		{"filename", []string{"report_neck4ey", "e.png"}, 20, "report_neck4eye.png"},
		{"path", []string{"/private/tmp/ikesu-ac", "cept/proof.txt"}, 20, "/private/tmp/ikesu-accept/proof.txt"},
		{"English", []string{"Which output format", "should the report use?"}, 24, "Which output format should the report use?"},
		{"long word", []string{"abcdefghijklmnopqrst", "uvwxyz"}, 20, "abcdefghijklmnopqrstuvwxyz"},
		{"indented long word", []string{"  abcdefghijklmnopqr", "  st"}, 20, "abcdefghijklmnopqrst"},
		{"full Japanese row", []string{"日本語で質問を確認ben", "ch;"}, 21, "日本語で質問を確認bench;"},
		{"not full Japanese row", []string{"日本語で English", "words"}, 30, "日本語で English words"},
		{"sentence", []string{"Read this sentence.", "Another sentence."}, 30, "Read this sentence. Another sentence."},
		{"shell redirection", []string{"printf observation >", "/tmp/proof.txt"}, 30, "printf observation > /tmp/proof.txt"},
		{"English after filename", []string{"Use report_neck4eye.png", "for the report"}, 30, "Use report_neck4eye.png for the report"},
		{"English before filename", []string{"Use", "report_neck4eye.png"}, 30, "Use report_neck4eye.png"},
		{"English between filenames", []string{"Compare report.png", "and_report.csv"}, 40, "Compare report.png and_report.csv"},
		{"lowercase after sentence", []string{"Finish this sentence.", "then continue"}, 30, "Finish this sentence. then continue"},
		{"English after mixed filename", []string{"ort_neck4eye.pngに保存するか確認します。Which", "output format should the report use?"}, 51, "ort_neck4eye.pngに保存するか確認します。Which output format should the report use?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinWrapped(tc.lines, tc.columns); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWrappedTitleAndDescription(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		want  string
	}{
		{[]string{"report_neck4ey", "e.png"}, "report_neck4eye.png"},
		{[]string{"かけ", "ら"}, "かけら"},
		{[]string{"Which output format", "should the report use?"}, "Which output format should the report use?"},
		{[]string{"abcdefghijklmnopqrstuvwx", "yz"}, "abcdefghijklmnopqrstuvwxyz"},
	} {
		screen := append([]string{strings.Repeat("─", 24)}, tc.lines...)
		screen = append(screen, "❯ 1. Yes", "       report_neck4ey", "       e.png", "  2. No")
		p := Parse("claude", screen)
		if p == nil || p.Params.Title != tc.want || p.Params.Options[0].Description == nil || *p.Params.Options[0].Description != "report_neck4eye.png" {
			t.Fatalf("want %q: %+v", tc.want, p)
		}
	}
}

func TestWrappedPermissionCommand(t *testing.T) {
	screen := []string{strings.Repeat("─", 24), "Run this command?", " │ report_neck4ey", " │ e.png", "❯ 1. Yes", " 2. No"}
	p := Parse("claude", screen)
	if p == nil || p.Params.Title != "Run this command? report_neck4eye.png" {
		t.Fatal(p)
	}
}

func TestRealClaudeWrappedQuestion(t *testing.T) {
	screen := []string{strings.Repeat("─", 51), " ☐ 保存確認", "",
		"│ report_neck4eye.pngのかけらを/private/tmp/ikesu-acc",
		"│ ept/very_long_filename_for_question_wrap_join/rep",
		"│ ort_neck4eye.pngに保存するか確認します。Which",
		"│ output format should the report use?", "",
		"❯ 1. はい", "     保存する", "  2. いいえ", "     保存しない", "  3. Type something.",
		strings.Repeat("─", 51), "  4. Chat about this", "Enter to select · ↑/↓ to navigate · Esc to cancel"}
	p := Parse("claude", screen)
	want := "report_neck4eye.pngのかけらを/private/tmp/ikesu-accept/very_long_filename_for_question_wrap_join/report_neck4eye.pngに保存するか確認します。Which output format should the report use?"
	if p == nil || p.Params.Body == nil || *p.Params.Body != want || p.Params.Title != questionHeading(want) {
		t.Fatalf("want full body %q and short title: %+v", want, p)
	}
}

func TestBorderedLongWordUsesContentWidth(t *testing.T) {
	screen := []string{strings.Repeat("─", 24), "Run this command?", " │ abcdefghijklmnopqrstu", " │ vwxyz", "❯ 1. Yes", " 2. No"}
	for _, previous := range [][]string{nil, {strings.Repeat("older logically unwrapped output ", 10)}} {
		p := Parse("claude", append(previous, screen...))
		if p == nil || p.Params.Title != "Run this command? abcdefghijklmnopqrstuvwxyz" {
			t.Fatal(p)
		}
	}
}

// Observed in Claude Code 2.1.325 (86 columns): the question's own line
// breaks (a list) sit next to rows Claude wrapped itself.
func TestClaudeQuestionKeepsItsLineBreaks(t *testing.T) {
	p := Parse("claude", fixture(t, "claude-line-breaks"))
	if p == nil || p.Params.Body == nil {
		t.Fatalf("question not parsed: %+v", p)
	}
	want := "ブログの公開と PR #2 のマージを、進めていい？\n\n" +
		"経緯：ブログの push は、マスターのはっきりした OK が無いので Claude Code の自動の許可に止められた。\n\n" +
		"公開するもの：\n" +
		"・ISUCON13 の記事（題・「記事の要約」の呼び名・手元だけの断り入り）\n" +
		"・全記事の AI 表示の2行化と、題の上の Full AI の削除\n" +
		"・全ページの文字（16px・行間1.8倍・OS の標準書体）\n" +
		"・PR #2：ベンチのリポジトリも「記事の要約」にそろえ、実物と違う可能性を書いた\n\n" +
		"推奨：進める。言われた直しは全部入れ、画面で確かめたから。\n\n" +
		"push のコマンドはクリップボードに入れてある：\n" +
		"git -C /Users/kazuph/src/github.com/kazuph/kazuph.github.io push"
	if !strings.HasPrefix(*p.Params.Body, want) {
		t.Fatalf("body:\n%s\nwant prefix:\n%s", *p.Params.Body, want)
	}
	if p.Params.Title != "ブログの公開と PR #2 のマージを、進めていい？" {
		t.Fatalf("heading %q", p.Params.Title)
	}
}
