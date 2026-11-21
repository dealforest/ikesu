package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/status"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestAgyForegroundStatusAfterCodexReport(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	dir := t.TempDir()
	agy := filepath.Join(dir, "agy")
	script := `#!/bin/sh
printf '⠇  Running command...\r\n────────────\r\n>\r\n────────────\r\n  ● [22:07:56] background task running\r\n────────────\r\nesc to cancel   auto · Gemini 4 Argon · low · 1 task(s) · /tasks\r\n'
read -r next
printf '\033[2J\033[H────────────\r\n>\r\n────────────\r\n  ● [22:07:56] background task running\r\n────────────\r\n? for shortcuts   auto · Gemini 4 Argon · low · 1 task(s) · /tasks\r\n'
read -r done
`
	if err := os.WriteFile(agy, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := a.innerRect(1)
	p, err := a.sup.SpawnWith(1, dir, r.W, r.H, nil, []string{agy})
	if err != nil {
		t.Fatal(err)
	}
	p.SetAgent("codex")
	a.SetReportedSource(1, "codex", "idle", "codex")

	waitScreen := func(text string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			p.PollProcess()
			if p.Snapshot().ForegroundAgent == "agy" && strings.Contains(p.ScreenText(), text) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("foreground agy did not paint %q: %+v\n%s", text, p.Snapshot(), p.ScreenText())
	}
	waitScreen("Running command")
	now := time.Now()
	a.detectPane(1, false, now)
	a.detectPane(1, false, now.Add(status.StartupGrace))
	assertReportedBlockerRender(t, a, "working")

	if _, err := p.WriteToPTY([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	waitScreen("? for shortcuts")
	a.detectPane(1, true, now.Add(status.StartupGrace+time.Millisecond))
	assertReportedBlockerRender(t, a, "idle")

	if p.Snapshot().AgentID != "codex" {
		t.Fatal("stored agent override was changed")
	}
	if agent, state, ok := a.Reported(1); !ok || agent != "codex" || state != "idle" {
		t.Fatalf("stored report was changed: %s %s %t", agent, state, ok)
	}
}
