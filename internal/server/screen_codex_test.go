package server

import (
	"os"

	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/screenprompt"
	"github.com/kazuph/ikesu/internal/session"
	"github.com/kazuph/ikesu/internal/teststate"
)

func codexQuestionTerminal(t *testing.T, fixture string) (*Server, *runtime.Pane, string) {
	t.Helper()
	s, p, input := screenTerminal(t, fixture, "codex")
	waitPromptCondition(t, func() bool {
		return screenprompt.Parse("codex", p.DetectionScreenLines()) != nil
	})
	return s, p, input
}

func TestCodexQuestionObservedCountdownKeepsDecisionID(t *testing.T) {
	teststate.Lock(t)
	s, p, input := codexQuestionTerminal(t, "codex-timed-first")
	scanScreen(s)
	d := screenRecord(t, s)
	if d.Body == nil || strings.ContainsAny(*d.Body, "0123456789") {
		t.Fatal("countdown body missing or contains numbers", d.Body)
	}
	for _, name := range []string{"codex-timed-countdown", "codex-timed-cursor", "codex-timed-free", "codex-timed-notes"} {
		paintScreen(p, screenDialog(t, name))
		scanScreen(s)
		got := screenRecord(t, s)
		if got.DecisionID != d.DecisionID || got.Title != d.Title || got.QuestionLabel == nil || *got.QuestionLabel != *d.QuestionLabel {
			t.Fatalf("%s changed saved question: %+v", name, got)
		}
	}
	if keys, err := os.ReadFile(input); err != nil || len(keys) != 0 {
		t.Fatalf("observation wrote PTY input: %q %v", keys, err)
	}
}

func TestCodexReusesLegacyPendingForSameRecordedQuestion(t *testing.T) {
	teststate.Lock(t)
	s, _, _ := codexQuestionTerminal(t, "codex-question")
	scanScreen(s)
	old := screenRecord(t, s)
	s.app.Do(func() {
		p := s.readScreenPrompt(1)
		w := &screenPromptWatch{prompt: p, signature: p.Signature(), codex: &codexBinding{identity: `["session","call","question"]`, question: session.CodexQuestion{Timestamp: time.UnixMilli(old.CreatedUnixMS - 1), State: session.CodexPending}}}
		s.screenPrompts.watches = map[int]*screenPromptWatch{1: w}
		s.beginScreenPrompt(1, w)
		if w.decisionID != old.DecisionID {
			t.Errorf("created %s instead of reusing %s", w.decisionID, old.DecisionID)
		}
	})
}

func TestCodexReconciliationKeepsLateAnswerBeforeRestore(t *testing.T) {
	teststate.Lock(t)
	s, _, _ := codexQuestionTerminal(t, "codex-question")
	scanScreen(s)
	late := screenRecord(t, s)
	s.app.Do(func() {
		p := s.readScreenPrompt(1)
		params := p.Params
		body := lateCodexQuestionBody
		params.Body = &body
		updated, _, err := s.store.UpdatePendingScreenDecision(late.DecisionID, params)
		if err != nil {
			t.Error(err)
			return
		}
		if screenprompt.FromDecision(updated) != p.Signature() {
			t.Error("fixture must match the currently visible question")
			return
		}
		w := &screenPromptWatch{prompt: p, signature: p.Signature(), codex: &codexBinding{identity: `["session","next-call","question"]`, question: session.CodexQuestion{Timestamp: time.UnixMilli(late.CreatedUnixMS - 1), State: session.CodexPending}}}
		s.screenPrompts.watches = map[int]*screenPromptWatch{1: w}
		s.beginScreenPrompt(1, w)
		if w.decisionID == "" || w.decisionID == late.DecisionID {
			t.Errorf("unrestored late decision reused: %s", w.decisionID)
		}
	})
	got, err := s.store.Decision(late.DecisionID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("unrestored late answer lost: %+v %v", got, err)
	}
}

func TestUnboundCodexQuestionRemainsPendingWhenScreenChanges(t *testing.T) {
	teststate.Lock(t)
	s, pane, input := codexQuestionTerminal(t, "codex-question")
	scanScreen(s)
	old := screenRecord(t, s)
	paintScreen(pane, screenDialog(t, "codex-question-last"))
	scanScreen(s)
	s.app.Do(func() {
		previous := s.screenPrompts.verifying[old.DecisionID]
		if previous == nil {
			t.Error("unbound question was not retained for transcript verification")
			return
		}
		if previous.w.codexAnchorPath != "" {
			t.Fatal("fixture unexpectedly has a session anchor")
		}
		if current := s.screenPrompts.watches[1]; current == nil || current.decisionID == old.DecisionID {
			t.Error("new question did not get a separate watch")
		}
		choice := old.Options[0].ID
		response := s.decisionRequest(&api.Request{ID: "answer", Method: api.MethodDecisionAnswer, Params: api.MarshalParams(api.DecisionAnswerParams{DecisionID: old.DecisionID, OptionID: &choice})})
		if response.Err == nil || response.Err.Code != api.ErrDecisionStale {
			t.Errorf("unverified original answer accepted: %+v", response.Err)
		}
	})
	for i := 0; i < 3; i++ {
		scanScreen(s)
	}
	s.app.Do(func() {
		if previous := s.screenPrompts.verifying[old.DecisionID]; previous == nil || previous.w.codexResult != nil {
			t.Error("unanchored old question started another transcript scan")
		}
	})
	got, err := s.store.Decision(old.DecisionID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("original decision changed: %+v %v", got, err)
	}
	if keys, err := os.ReadFile(input); err != nil || len(keys) != 0 {
		t.Fatalf("observation wrote PTY input: %q %v", keys, err)
	}
}
