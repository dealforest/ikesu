package server

import (
	"strings"
	"testing"
	"time"
)

// Lines follow the records Claude Code 2.1 writes: the assistant's Bash
// tool_use, and the queue record of the resume notice (plus the user message
// copy of the same notice, which must not count twice).
func TestReadInterruptedBackgroundListsOnlyNewResumeNotices(t *testing.T) {
	t.Parallel()
	notice := func(at, id string) string {
		return `{"type":"queue-operation","operation":"enqueue","timestamp":"` + at + `","content":"<task-notification>\n<task-id>b1</task-id>\n<tool-use-id>` + id + `</tool-use-id>\n<status>stopped</status>\n<summary>Background shell command didn't finish before the previous session ended</summary>\n</task-notification>"}`
	}
	tool := func(id, desc, cmd string, background bool) string {
		bg := "false"
		if background {
			bg = "true"
		}
		return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":"` + cmd + `","description":"` + desc + `","run_in_background":` + bg + `}}]}}`
	}
	transcript := strings.Join([]string{
		tool("toolu_serve", "Serve the player locally", "python3 -m http.server 5810", true),
		tool("toolu_old", "Old watcher", "./watch.sh", true),
		tool("toolu_fg", "Foreground", "ls", false),
		notice("2026-11-04T10:00:00Z", "toolu_old"),
		notice("2026-11-14T21:58:53.518Z", "toolu_serve"),
		`{"type":"user","message":{"role":"user","content":"<task-notification>\n<tool-use-id>toolu_serve</tool-use-id>\n<summary>Background shell command didn't finish before the previous session ended</summary>"}}`,
		notice("2026-11-14T21:58:54Z", "toolu_serve"),
		notice("2026-11-14T21:58:55Z", "toolu_fg"),
		notice("2026-11-14T21:58:56Z", "toolu_missing"),
	}, "\n")
	since, _ := time.Parse(time.RFC3339, "2026-11-14T21:50:00Z")
	got, err := readInterruptedBackground(strings.NewReader(transcript), since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ToolUseID != "toolu_serve" || got[0].Command != "python3 -m http.server 5810" || got[0].Description != "Serve the player locally" {
		t.Fatalf("interrupted commands = %+v", got)
	}
}

func TestInterruptedBackgroundMessageAsksToRestartOnlyLongRunning(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", interruptedCommandMax+20)
	msg := interruptedBackgroundMessage([]interruptedCommand{
		{ToolUseID: "toolu_a", Description: "Serve the player", Command: "python3 -m http.server\n  5810"},
		{ToolUseID: "toolu_b", Command: long},
	})
	for _, want := range []string{"2 件", "起動し直して", "流し直さず", "- Serve the player（toolu_a）: python3 -m http.server 5810", "- 説明なし（toolu_b）: " + strings.Repeat("x", interruptedCommandMax) + "…"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message lacks %q:\n%s", want, msg)
		}
	}
}
