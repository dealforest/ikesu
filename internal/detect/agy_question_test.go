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
	for _, tc := range []struct {
		prefix string
		state  State
	}{{"Requesting permission for: command\nDo you want to proceed?\n", Blocked}, {"", Working}} {
		screen := tc.prefix + idle
		if tc.state == Working {
			screen += "· 1 task\n"
		}
		if d := Detect("agy", Input{Screen: screen}); d.State != tc.state {
			t.Fatalf("priority: %+v", d)
		}
	}
	b, _ := os.ReadFile("../screenprompt/testdata/agy-question.txt")
	if d := Detect("agy", Input{Screen: idle + string(b)}); d.State != Blocked {
		t.Fatalf("question priority: %+v", d)
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
