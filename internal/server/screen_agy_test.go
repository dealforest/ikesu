package server

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/screenprompt"

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

func TestAgyEditorInputStopsOnChange(t *testing.T) {
	for _, stage := range []string{"text", "confirm"} {
		for _, cause := range []string{"question", "editor", "external input"} {
			t.Run(stage+"/"+cause, func(t *testing.T) {
				teststate.Lock(t)
				s, pane, input := screenTerminal(t, "agy-question", "agy")
				original := screenprompt.Parse("agy", strings.Split(screenDialog(t, "agy-question"), "\n"))
				s.app.Do(func() {
					if s.screenPrompts.watches == nil {
						s.screenPrompts.watches = map[int]*screenPromptWatch{}
					}
					s.screenPrompts.watches[1] = &screenPromptWatch{prompt: original, editingText: true, answerInputSequence: pane.ExternalInputSequence()}
				})
				editor := screenDialog(t, "agy-question")
				editor = strings.Replace(editor, "> 1.", "  1.", 1)
				editor = strings.Replace(editor, "  3.", "> 3.", 1)
				editor = strings.Replace(editor, "↑/↓ Navigate · → Next · enter Select · esc Skip · ctrl+r Review", "Your answer:\n  draft\n\n\n\n  enter Submit · esc Back", 1)
				if cause == "question" {
					editor = strings.Replace(editor, "Question 1/4:", "Question 2/4:", 1)
				}
				if cause == "editor" {
					editor = strings.Replace(editor, "enter Submit · esc Back", "Review", 1)
				}
				paintScreen(pane, editor)
				if cause == "external input" {
					pane.RecordExternalInput()
				}
				open := true
				data := "text"
				if stage == "confirm" {
					data = "\r"
				}
				expected := "draft"
				if stage == "text" {
					expected = ""
				}
				if err := s.screenInputCheckedPaste(1, original.Signature(), data, true, &open, nil, stage == "text", &expected); err == nil {
					t.Fatal("changed state accepted")
				}
				if b, _ := os.ReadFile(input); len(b) != 0 {
					t.Fatalf("unexpected input %q", b)
				}
			})
		}
	}
}

func TestAgyRejectsUnsafeWriteIn(t *testing.T) {
	for _, text := range []string{"", "escape\x1b[A", "return\r"} {
		t.Run(text, func(t *testing.T) {
			teststate.Lock(t)
			s, _, input := screenTerminal(t, "agy-question", "agy")
			scanScreen(s)
			d := screenRecord(t, s)
			if r := answerScreen(s, d, "", &text); r.Err == nil {
				t.Fatal("unsafe write-in accepted")
			}
			if b, _ := os.ReadFile(input); len(b) != 0 {
				t.Fatalf("unexpected input %q", b)
			}
		})
	}
	t.Run("text and option", func(t *testing.T) {
		teststate.Lock(t)
		s, _, input := screenTerminal(t, "agy-question", "agy")
		scanScreen(s)
		d := screenRecord(t, s)
		text := "custom"
		if r := answerScreen(s, d, "1", &text); r.Err == nil {
			t.Fatal("option and write-in accepted together")
		}
		if b, _ := os.ReadFile(input); len(b) != 0 {
			t.Fatalf("unexpected input %q", b)
		}
	})
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

func TestAgyWriteInPTY(t *testing.T) {
	for _, tc := range []struct {
		name, text                                  string
		bracketed, interrupt, occupied, final, fail bool
	}{
		{"single", "custom answer", false, false, false, false, false},
		{"multiline paste", "first\nsecond", true, false, false, false, false},
		{"final multiline paste", "first\nsecond", true, false, false, true, false},
		{"multiline without paste", "first\nsecond", false, false, false, false, true},
		{"changed question", "custom answer", false, true, false, false, true},
		{"occupied editor", "custom answer", false, false, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teststate.Lock(t)
			s, pane, input := screenTerminal(t, "agy-question", "agy")
			if tc.final {
				final := strings.Replace(screenDialog(t, "agy-question"), "Question 1/4:", "Question 4/4:", 1)
				final = strings.Replace(final, "enter Select", "enter Submit All", 1)
				paintScreen(pane, final)
			}
			scanScreen(s)
			d := screenRecord(t, s)
			text := tc.text
			done := make(chan api.Response, 1)
			go func() { done <- answerScreen(s, d, "", &text) }()
			picture := screenDialog(t, "agy-question")
			if tc.final {
				picture = strings.Replace(picture, "Question 1/4:", "Question 4/4:", 1)
				picture = strings.Replace(picture, "enter Select", "enter Submit All", 1)
			}
			waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\x1b[B" })
			picture = strings.Replace(picture, "> 1.", "  1.", 1)
			picture = strings.Replace(picture, "  2.", "> 2.", 1)
			paintScreen(pane, picture)
			waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\x1b[B\x1b[B" })
			picture = strings.Replace(picture, "> 2.", "  2.", 1)
			picture = strings.Replace(picture, "  3.", "> 3.", 1)
			picture = strings.Replace(picture, "enter Select", "enter Write-in", 1)
			if tc.final {
				picture = strings.Replace(picture, "enter Submit All", "enter Write-in", 1)
			}
			paintScreen(pane, picture)
			waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == "\x1b[B\x1b[B\r" })
			editor := strings.Replace(picture, "↑/↓ Navigate · → Next · enter Write-in · esc Skip · ctrl+r Review", "Your answer:\n\n\n\n\n  enter Submit · esc Back", 1)
			if tc.final {
				editor = strings.Replace(editor, "enter Submit · esc Back", "enter Submit All · esc Back", 1)
			}
			if tc.interrupt {
				editor = strings.Replace(editor, "Question 1/4:", "Question 2/4:", 1)
			}
			if tc.occupied {
				editor = strings.Replace(editor, "Your answer:", "Your answer:\n  existing draft", 1)
			}
			if tc.bracketed {
				editor = "\x1b[?2004h" + editor
			}
			paintScreen(pane, editor)
			if !tc.fail {
				prefix := "\x1b[B\x1b[B\r"
				if tc.bracketed {
					prefix += "\x1b[200~" + text + "\x1b[201~"
				} else {
					prefix += text
				}
				waitPromptCondition(t, func() bool { b, _ := os.ReadFile(input); return string(b) == prefix })
				paintScreen(pane, strings.Replace(editor, "Your answer:", "Your answer:\n  "+text, 1))
			}
			select {
			case r := <-done:
				if (r.Err != nil) != tc.fail {
					t.Fatalf("response: %+v", r.Err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("delivery timed out")
			}
			b, _ := os.ReadFile(input)
			want := "\x1b[B\x1b[B\r"
			if !tc.fail {
				if tc.bracketed {
					want += "\x1b[200~" + text + "\x1b[201~\r"
				} else {
					want += text + "\r"
				}
			}
			if string(b) != want {
				t.Fatalf("input %q, want %q", b, want)
			}
		})
	}
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
