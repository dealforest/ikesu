package screenprompt

import (
	"os"
	"strings"
	"testing"
)

func TestAgyQuestionObservedForm(t *testing.T) {
	data, err := os.ReadFile("testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := string(data)
	// The real layout has a blank row between the rule and question.
	p := Parse("agy", strings.Split(screen, "\n"))
	if p == nil {
		t.Fatal("question not detected")
	}
	if !p.AgyQuestion || p.Question != 1 || p.Total != 4 || p.Cursor != 0 || len(p.Params.Options) != 2 || p.Params.Options[0].Label != "(Recommended) Plain text" || !p.Params.AllowText || p.TextID != "3" {
		t.Fatalf("unexpected prompt: %+v", p)
	}
	next := Parse("agy", strings.Split(strings.Replace(screen, "Question 1/4:", "Question 2/4:", 1), "\n"))
	if next == nil || next.Signature() == p.Signature() {
		t.Fatal("question number must change identity")
	}
	for _, s := range []string{strings.Replace(screen, "esc Skip", "other", 1), strings.Replace(screen, "Question\n─", "Permission\n─", 1), strings.Replace(screen, "3. Write-in...", "3. Other", 1), strings.Replace(screen, "1. (Recommended)", "1. [ ] (Recommended)", 1), strings.Replace(screen, "enter Select", "space Toggle", 1)} {
		if Parse("agy", strings.Split(s, "\n")) != nil {
			t.Fatalf("accepted incomplete or unrelated screen: %s", s)
		}
	}
	withoutReview := strings.Replace(screen, " · ctrl+r Review", "", 1)
	if Parse("agy", strings.Split(withoutReview, "\n")) == nil {
		t.Fatal("question without Review was rejected")
	}
}

func TestAgyWriteInSelected(t *testing.T) {
	b, err := os.ReadFile("testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := strings.Replace(string(b), "> 1.", "  1.", 1)
	screen = strings.Replace(screen, "  3.", "> 3.", 1)
	screen = strings.Replace(screen, "enter Select", "enter Write-in", 1)
	p := Parse("agy", strings.Split(screen, "\n"))
	if p == nil || p.Cursor != 2 || p.TextID != "3" || !p.Params.AllowText {
		t.Fatalf("write-in: %+v", p)
	}
}

func TestAgyWriteInEditor(t *testing.T) {
	b, err := os.ReadFile("testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := strings.Replace(string(b), "> 1.", "  1.", 1)
	screen = strings.Replace(screen, "  3.", "> 3.", 1)
	screen = strings.Replace(screen, "↑/↓ Navigate · → Next · enter Select · esc Skip · ctrl+r Review", "Your answer:\n  custom answer\n\n\n\n  enter Submit · esc Back", 1)
	p := Parse("agy", strings.Split(screen, "\n"))
	if p == nil || !p.TextOpen || p.TextID != "3" {
		t.Fatalf("editor: %+v", p)
	}
	if Parse("agy", strings.Split(strings.Replace(screen, "enter Submit · esc Back", "Review", 1), "\n")) != nil {
		t.Fatal("accepted non-editor")
	}
	final := strings.Replace(screen, "Question 1/4:", "Question 4/4:", 1)
	final = strings.Replace(final, "enter Submit · esc Back", "enter Submit All · esc Back", 1)
	if p := Parse("agy", strings.Split(final, "\n")); p == nil || !p.TextOpen || p.Question != 4 {
		t.Fatalf("final editor: %+v", p)
	}
	if Parse("agy", strings.Split(strings.Replace(final, "Question 4/4:", "Question 1/4:", 1), "\n")) != nil {
		t.Fatal("accepted Submit All editor on a non-final question")
	}
}

func TestAgyFinalQuestionFooter(t *testing.T) {
	data, err := os.ReadFile("testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := strings.Replace(string(data), "Question 1/4:", "Question 4/4:", 1)
	for _, label := range []string{"Submit All", "Submit"} {
		final := strings.Replace(screen, "enter Select", "enter "+label, 1)
		if p := Parse("agy", strings.Split(final, "\n")); p == nil || p.Question != 4 {
			t.Fatalf("%s not parsed", label)
		}
	}
	for _, footer := range []string{"enter Select", "enter Submit All Extra", "enter Toggle"} {
		final := strings.Replace(screen, "enter Select", footer, 1)
		if Parse("agy", strings.Split(final, "\n")) != nil {
			t.Fatalf("accepted final footer %s", footer)
		}
	}
}

// agy 1.5.17 で1問だけ聞いた画面（2026-11-26 に実物から保存）。Next も Submit も無く、enter で答える。
func TestAgySingleQuestionObservedForm(t *testing.T) {
	data, err := os.ReadFile("testdata/agy-question-single.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := string(data)
	p := Parse("agy", strings.Split(screen, "\n"))
	if p == nil {
		t.Fatal("single question not detected")
	}
	if !p.AgyQuestion || p.Question != 1 || p.Total != 1 || p.Cursor != 0 || len(p.Params.Options) != 3 ||
		p.Params.Options[1].Label != "ラーメン" || p.TextID != "4" || p.Params.Title != "今日の夕飯はどれにする？" {
		t.Fatalf("unexpected prompt: %+v", p)
	}
	// The short footer belongs only to a lone question: in a longer set the last one shows Submit.
	if Parse("agy", strings.Split(strings.Replace(screen, "Question 1/1:", "Question 2/2:", 1), "\n")) != nil {
		t.Fatal("accepted the short footer on the last of several questions")
	}
	// On the Write-in row the last question of any set is answerable, the same as detection decides.
	for _, header := range []string{"Question 1/1:", "Question 2/2:"} {
		s := strings.Replace(screen, "Question 1/1:", header, 1)
		s = strings.Replace(strings.Replace(s, "> 1.", "  1.", 1), "  4. Write-in", "> 4. Write-in", 1)
		s = strings.Replace(s, "enter Select", "enter Write-in", 1)
		if p := Parse("agy", strings.Split(s, "\n")); p == nil || p.Cursor != 3 {
			t.Fatalf("%s on Write-in: %+v", header, p)
		}
	}
	if Parse("agy", strings.Split(strings.TrimRight(screen, "\n")+"\nGemini finished\n", "\n")) != nil {
		t.Fatal("old question kept after ordinary output")
	}
	for _, footer := range []string{"↑/↓ Navigate · → Next · enter Select · esc Skip", "↑/↓ Navigate · space Toggle · esc Skip", "↑/↓ Navigate · enter Select"} {
		if Parse("agy", strings.Split(strings.Replace(screen, "↑/↓ Navigate · enter Select · esc Skip", footer, 1), "\n")) != nil {
			t.Fatalf("accepted footer %q", footer)
		}
	}
}

func TestAgyRejectsStaleQuestionWithLaterScreen(t *testing.T) {
	b, err := os.ReadFile("testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"Review\nSubmit", "────────\n>\n────────\n? for shortcuts", "Unknown screen", "esc to cancel   auto · Model · high"} {
		if Parse("agy", strings.Split(string(b)+"\n"+suffix, "\n")) != nil {
			t.Fatalf("accepted following screen %q", suffix)
		}
	}
	quota := "Gemini 5h 17% (2h50m) · wk 15% (6d16h) │ Claude/GPT 5h 0% (4h20m) · wk 0% (6d23h) │ Argon wk 0% (6d23h)"
	if Parse("agy", strings.Split(string(b)+"\n"+quota+"\n", "\n")) == nil {
		t.Fatal("observed quota rejected")
	}
}
