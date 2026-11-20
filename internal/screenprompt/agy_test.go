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
	if !p.AgyQuestion || p.Question != 1 || p.Total != 4 || p.Cursor != 0 || len(p.Params.Options) != 2 || p.Params.Options[0].Label != "(Recommended) Plain text" || p.Params.AllowText {
		t.Fatalf("unexpected prompt: %+v", p)
	}
	next := Parse("agy", strings.Split(strings.Replace(screen, "Question 1/4:", "Question 2/4:", 1), "\n"))
	if next == nil || next.Signature() == p.Signature() {
		t.Fatal("question number must change identity")
	}
	for _, s := range []string{strings.Replace(screen, "ctrl+r Review", "other", 1), strings.Replace(screen, "Question\n─", "Permission\n─", 1), strings.Replace(screen, "3. Write-in...", "3. Other", 1), strings.Replace(screen, "1. (Recommended)", "1. [ ] (Recommended)", 1), strings.Replace(screen, "enter Select", "space Toggle", 1)} {
		if Parse("agy", strings.Split(s, "\n")) != nil {
			t.Fatalf("accepted incomplete or unrelated screen: %s", s)
		}
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
