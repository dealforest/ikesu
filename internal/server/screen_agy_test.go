package server

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"

	"github.com/kazuph/ikesu/internal/teststate"
)

func TestAgyQuestionPTYAnswerAndStale(t *testing.T) {
	teststate.Lock(t)
	s, pane, input := screenTerminal(t, "agy-question", "agy")
	scanScreen(s)
	d := screenRecord(t, s)
	if d.Origin == nil || d.Origin.Agent != "agy" || len(d.Options) != 2 {
		t.Fatalf("bad decision: %+v", d)
	}
	// A changed question must refuse input, even if its options are identical.
	paintScreen(pane, strings.Replace(screenDialog(t, "agy-question"), "Question 1/4:", "Question 2/4:", 1))
	if r := answerScreen(s, d, "1", nil); r.Err == nil {
		t.Fatal("stale answer accepted")
	}
	if b, _ := os.ReadFile(input); len(b) != 0 {
		t.Fatalf("stale answer wrote %q", b)
	}
	scanScreen(s)
	scanScreen(s)
	d = screenRecord(t, s)
	if r := answerScreen(s, d, "1", nil); r.Err != nil {
		t.Fatalf("answer: %+v", r.Err)
	}
	waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\r" })
}

func TestAgyPTYNavigatesThenEntersOnce(t *testing.T) {
	teststate.Lock(t)
	s, pane, input := screenTerminal(t, "agy-question", "agy")
	scanScreen(s)
	d := screenRecord(t, s)
	done := make(chan api.Response, 1)
	go func() { done <- answerScreen(s, d, "2", nil) }()
	waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\x1b[B" })
	picture := screenDialog(t, "agy-question")
	picture = strings.Replace(picture, "> 1.", "  1.", 1)
	picture = strings.Replace(picture, "  2.", "> 2.", 1)
	paintScreen(pane, picture)
	select {
	case r := <-done:
		if r.Err != nil {
			t.Fatalf("answer: %+v", r.Err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("delivery timed out")
	}
	waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\x1b[B\r" })
}

func TestAgyFinalQuestionPTYEntersOnlyOnce(t *testing.T) {
	teststate.Lock(t)
	s, pane, input := screenTerminal(t, "agy-question", "agy")
	picture := strings.Replace(screenDialog(t, "agy-question"), "Question 1/4:", "Question 4/4:", 1)
	picture = strings.Replace(picture, "enter Select", "enter Submit All", 1)
	paintScreen(pane, picture)
	scanScreen(s)
	d := screenRecord(t, s)
	if r := answerScreen(s, d, "1", nil); r.Err != nil {
		t.Fatalf("final answer: %+v", r.Err)
	}
	waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\r" })
}

func TestAgyStaleQuestionLaterScreenPTYNoInput(t *testing.T) {
	for _, suffix := range []string{"Review\nSubmit", "────────\n>\n────────\n? for shortcuts", "Unknown screen"} {
		t.Run(suffix, func(t *testing.T) {
			teststate.Lock(t)
			s, pane, input := screenTerminal(t, "agy-question", "agy")
			scanScreen(s)
			d := screenRecord(t, s)
			paintScreen(pane, screenDialog(t, "agy-question")+"\n"+suffix)
			if r := answerScreen(s, d, "1", nil); r.Err == nil {
				t.Fatal("stale answer accepted")
			}
			if b, _ := os.ReadFile(input); len(b) > 0 {
				t.Fatalf("unexpected input %q", b)
			}
		})
	}
}
