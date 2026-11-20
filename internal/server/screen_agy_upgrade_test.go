package server

import (
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestAgyWriteInReplacesOptionsOnlyDialog(t *testing.T) {
	teststate.Lock(t)
	s, _, _ := screenTerminal(t, "agy-question", "agy")
	var old, unrelated api.Decision
	s.app.Do(func() {
		prompt := s.readScreenPrompt(1)
		if prompt == nil || !prompt.Params.AllowText {
			t.Error("expected agy write-in question")
			return
		}
		store, err := s.db()
		if err != nil {
			t.Error(err)
			return
		}
		params := prompt.Params
		params.AllowText = false
		params.Origin = &api.DecisionOrigin{Machine: "local", Agent: "agy", PaneID: "p1", Reason: "screen_prompt"}
		old, err = store.CreateDecision(params, time.Now())
		if err != nil {
			t.Error(err)
			return
		}
		params.QuestionLabel = new("Question 2/4")
		unrelated, err = store.CreateDecision(params, time.Now())
		if err != nil {
			t.Error(err)
		}
	})
	if t.Failed() {
		return
	}
	scanScreen(s)
	current := screenRecord(t, s)
	if !current.AllowText || current.DecisionID == old.DecisionID || len(current.Options) != 2 {
		t.Fatalf("expected a new dialog with text input: %+v", current)
	}
	for id, want := range map[string]string{old.DecisionID: "cancelled", unrelated.DecisionID: "pending"} {
		d, err := s.store.Decision(id)
		if err != nil || d.Status != want {
			t.Fatalf("decision %s: status %q, want %q, error %v", id, d.Status, want, err)
		}
	}
}
