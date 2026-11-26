package detect

import (
	"os"
	"strings"
	"testing"
)

func TestAgyQuestionDetection(t *testing.T) {
	b, err := os.ReadFile("../screenprompt/testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := string(b)
	if d := Detect("agy", Input{Screen: screen}); d.State != Blocked || d.RuleID != "question_prompt" {
		t.Fatalf("question: %+v", d)
	}
	for _, s := range []string{strings.Replace(screen, "Question 1/4:", "Permission:", 1), strings.Replace(screen, "esc Skip", "", 1)} {
		if d := Detect("agy", Input{Screen: s}); d.RuleID == "question_prompt" {
			t.Fatalf("false question: %+v", d)
		}
	}
	if d := Detect("agy", Input{Screen: strings.Replace(screen, " · ctrl+r Review", "", 1)}); d.State != Blocked {
		t.Fatalf("question without Review: %+v", d)
	}
}

// 1問だけの質問（agy 1.5.17・1.5.18、2026-11-26 に実物で確認）は Next が無い。下に使用量の行が続いても待ちにする。
func TestAgySingleQuestionDetection(t *testing.T) {
	b, err := os.ReadFile("../screenprompt/testdata/agy-question-single.txt")
	if err != nil {
		t.Fatal(err)
	}
	screen := string(b)
	quota := "Gemini 5h 3% (2h31m) · wk 6% (6d16h) │ Claude/GPT 5h 0% (<1m) · wk 50% (1d22h)\n"
	for name, s := range map[string]string{"plain": screen, "with quota": strings.TrimRight(screen, "\n") + "\n" + quota} {
		if d := Detect("agy", Input{Screen: s}); d.State != Blocked || d.RuleID != "question_prompt" {
			t.Fatalf("%s: %+v", name, d)
		}
	}
	if d := Detect("agy", Input{Screen: strings.Replace(screen, "esc Skip", "", 1)}); d.RuleID == "question_prompt" {
		t.Fatalf("false question: %+v", d)
	}
	// The short footer waits only under 1/1, the same rule the reader uses: 1/2 and 2/2 with it
	// are neither waiting nor answerable.
	for _, header := range []string{"Question 1/2:", "Question 2/2:"} {
		s := strings.Replace(screen, "Question 1/1:", header, 1)
		if d := Detect("agy", Input{Screen: s}); d.RuleID == "question_prompt" {
			t.Fatalf("%s with the short footer detected as waiting: %+v", header, d)
		}
	}
	// Ordinary output under the status row ends the question.
	if d := Detect("agy", Input{Screen: strings.TrimRight(screen, "\n") + "\nGemini finished\n"}); d.RuleID == "question_prompt" {
		t.Fatalf("old question kept after output: %+v", d)
	}
}

func TestAgyIdleAndActivePriority(t *testing.T) {
	idle := "──────────\n>\n──────────\n? for shortcuts                         auto · Gemini 4 Neon · medium\n"
	d := Detect("agy", Input{Screen: idle})
	if d.State != Idle || !d.VisibleIdle || d.RuleID != "idle_prompt" {
		t.Fatalf("idle: %+v", d)
	}
	for _, footer := range []string{"? for shortcuts · manual · Other Model · high", "? for shortcuts", "? for shortcuts · auto · Model\nquota 20%"} {
		screen := "──────────\n>\n──────────\n" + footer + "\n"
		if d := Detect("agy", Input{Screen: screen}); d.State != Idle || !d.VisibleIdle {
			t.Fatalf("alternate idle: %+v", d)
		}
	}
	for _, bad := range []string{strings.Replace(idle, ">\n", "> draft\n", 1), strings.Replace(idle, "? for shortcuts", "other", 1), strings.Replace(idle, "──────────\n>\n", "text\n>\n", 1)} {
		if d := Detect("agy", Input{Screen: bad}); d.VisibleIdle {
			t.Fatalf("false idle: %+v", d)
		}
	}
	if d := Detect("agy", Input{Screen: "Requesting permission for: command\nDo you want to proceed?\n" + idle}); d.State != Blocked {
		t.Fatalf("permission priority: %+v", d)
	}
	b, _ := os.ReadFile("../screenprompt/testdata/agy-question.txt")
	if d := Detect("agy", Input{Screen: idle + string(b)}); d.State != Blocked {
		t.Fatalf("question priority: %+v", d)
	}
}

func TestAgyBackgroundTasksLeavePromptIdle(t *testing.T) {
	// agy displays the task panel below the blank input box.
	prompt := "──────────\n>\n──────────\n"
	task := "  ● [16:18:52] npm run storybook running\n"
	footer := "──────────\n? for shortcuts    auto · Gemini 4 Neon · medium · 1 task(s) · /tasks\n"
	quota := "Gemini 5h 21% (<1m) · wk 17% (6d10h) │ Claude/GPT 5h 0% (<1m) · wk 0% (6d18h) │ Argon wk 0% (6d18h)\n"
	idle := prompt + task + footer + quota
	for _, screen := range []string{idle, prompt + strings.Repeat(task, 5) + strings.Replace(footer, "1 task(s)", "5 task(s)", 1)} {
		d := Detect("agy", Input{Screen: screen})
		if d.State != Idle || !d.VisibleIdle || d.VisibleWorking {
			t.Fatalf("background tasks with idle prompt: %+v", d)
		}
	}
	for _, screen := range []string{footer + quota, idle + "unknown screen\n", strings.Replace(idle, ">\n", "> unsent draft\n", 1)} {
		d := Detect("agy", Input{Screen: screen})
		if d.VisibleIdle || d.VisibleWorking {
			t.Fatalf("task footer alone must not signal a state: %+v", d)
		}
	}
	for _, tc := range []struct {
		prefix string
		state  State
	}{
		{"⣻  Working...\n", Working},
		{"Requesting permission for: command\nDo you want to proceed?\n", Blocked},
	} {
		if d := Detect("agy", Input{Screen: tc.prefix + idle}); d.State != tc.state {
			t.Fatalf("foreground priority: %+v", d)
		}
	}
}

func TestAgyFinalQuestionDetection(t *testing.T) {
	b, err := os.ReadFile("../screenprompt/testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Submit All", "Submit"} {
		screen := strings.Replace(strings.Replace(string(b), "Question 1/4:", "Question 4/4:", 1), "enter Select", "enter "+label, 1)
		if d := Detect("agy", Input{Screen: screen}); d.State != Blocked {
			t.Fatalf("%s: %+v", label, d)
		}
	}
}

func TestAgyOldQuestionDoesNotBlockNewScreen(t *testing.T) {
	b, err := os.ReadFile("../screenprompt/testdata/agy-question.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"Review\nSubmit", "────────\n>\n────────\n? for shortcuts", "Unknown screen"} {
		if d := Detect("agy", Input{Screen: string(b) + "\n" + suffix}); d.RuleID == "question_prompt" {
			t.Fatalf("stale blocked: %+v", d)
		}
	}
}
