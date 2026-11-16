package render

import (
	"encoding/json"
	"testing"
)

// New worktree takes a branch name, so the phone needs an input field and
// the checkout path, although the dialog is not an InputEdit one.
func TestNativeMenuJSONWorktreeCreateHasInput(t *testing.T) {
	var got struct {
		Input *string  `json:"input"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(NativeMenuJSON(nil, &Dialog{Title: "New worktree", WorktreeCreate: true, Input: "feat", Checkout: "/w/feat"})), &got); err != nil {
		t.Fatal(err)
	}
	if got.Input == nil || *got.Input != "feat" || len(got.Lines) != 1 || got.Lines[0] != "/w/feat" {
		t.Fatalf("worktree dialog payload = %+v", got)
	}
}
