package server

import (
	"os"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/screenprompt"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestDisabledScreenPromptReloadSkipsPendingScan(t *testing.T) {
	teststate.Lock(t)
	s := newTestServer(t, "disabled")
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := dispatch.Open(dispatch.DBPath(s.session))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s.initDecisionHub()
	defer s.stopDecisions()
	disabled := false
	cfg := config.Config{}
	cfg.Decisions.ScreenPrompts = &disabled
	s.screenPromptConfig(cfg)
	if !s.screenPrompts.disabledCleaned || !s.decisions.pendingCacheValid {
		t.Fatal("initial disabled cleanup did not load the pending cache")
	}
	version := s.decisions.pendingCacheDataVersion
	d, err := other.CreateDecision(api.DecisionCreateParams{
		Kind: "agent_prompt", Title: "External pending screen question",
		Origin:  &api.DecisionOrigin{PaneID: "p1", Reason: "screen_prompt"},
		Options: []api.DecisionOption{{ID: "one", Label: "One", Role: "other"}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if current, err := store.DataVersion(); err != nil || current == version {
		t.Fatalf("external commit did not change data_version: got %d, err=%v", current, err)
	}
	for range 3 {
		s.screenPromptConfig(cfg)
		if !s.decisions.pendingCacheValid || s.decisions.pendingCacheDataVersion != version {
			t.Fatal("disabled reload reloaded or invalidated the cache")
		}
	}
	if got, err := store.Decision(d.DecisionID); err != nil || got.Status != "pending" {
		t.Fatalf("disabled reload scanned external pending rows: %+v, err=%v", got, err)
	}
	enabled := true
	onCfg := config.Config{}
	onCfg.Decisions.ScreenPrompts = &enabled
	s.screenPromptConfig(onCfg)
	s.screenPromptConfig(cfg)
	if got, err := store.Decision(d.DecisionID); err != nil || got.Status != "cancelled" {
		t.Fatalf("ON to OFF did not clean pending screen decisions: %+v, err=%v", got, err)
	}
}

func TestScreenPromptReenableStartsFreshFlowAndPreview(t *testing.T) {
	teststate.Lock(t)
	s, pane, input := screenTerminal(t, "claude-final-confirm", "claude")
	final := screenprompt.Parse("claude", screenpromptLines(t, "claude-final-confirm"))
	preview := screenprompt.Parse("claude", screenpromptLines(t, "claude-preview"))
	if final == nil || final.Flow == "" || preview == nil || !preview.HasPreview {
		t.Fatal("screen prompt fixtures did not parse")
	}
	s.app.Do(func() {
		s.screenPrompts.flows = map[int]*screenAnswerFlow{1: {
			name: final.Flow, total: 2, inputSequence: pane.ExternalInputSequence(), answered: map[int]bool{0: true, 1: true},
		}}
		s.markPreviewSeen(1, preview)
		disabled := false
		cfg := config.Config{}
		cfg.Decisions.ScreenPrompts = &disabled
		enabled := true
		onCfg := config.Config{}
		onCfg.Decisions.ScreenPrompts = &enabled
		s.screenPromptConfig(cfg)
		s.screenPromptConfig(onCfg)
		s.scanScreenPrompts()
		if s.screenPrompts.flows[1] != nil {
			t.Error("same-name final screen reused old answers")
		}
		paintScreen(pane, screenDialog(t, "claude-preview"))
		w := &screenPromptWatch{prompt: preview}
		s.beginScreenPrompt(1, w)
		if w.preview == nil {
			t.Error("previous preview signature suppressed the new scan")
		}
	})
	waitPromptCondition(t, func() bool {
		keys, _ := os.ReadFile(input)
		return len(keys) != 0
	})
	if keys, err := os.ReadFile(input); err != nil || string(keys) != "\x1b[B" {
		t.Fatalf("expected just the fresh preview arrow, got %q, err=%v", keys, err)
	}
}
