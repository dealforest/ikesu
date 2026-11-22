package server

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/session"
	"github.com/kazuph/ikesu/internal/teststate"
	"github.com/kazuph/ikesu/internal/tui"
)

const agyTestA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
const agyTestB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

// This child holds real files under a temporary HOME and responds through its PTY.
func TestAgySessionPTYHelper(t *testing.T) {
	if os.Getenv("AGY_SESSION_PTY_HELPER") != "1" {
		return
	}
	root := filepath.Join(os.Getenv("AGY_SESSION_HOME"), ".gemini", "antigravity-cli", "presence")
	if err := os.MkdirAll(root, 0700); err != nil {
		os.Exit(2)
	}
	var open []*os.File
	fmt.Println("helper-ready")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := strings.TrimSpace(scanner.Text())
		for _, f := range open {
			f.Close()
		}
		open = nil
		var names []string
		switch command {
		case "first", "again":
			names = []string{agyTestA}
		case "second":
			names = []string{agyTestB}
		case "both":
			names = []string{agyTestA, agyTestB}
		case "quit":
			return
		}
		for _, id := range names {
			f, err := os.OpenFile(filepath.Join(root, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				os.Exit(3)
			}
			open = append(open, f)
		}
		fmt.Println("helper-" + command)
	}
}

func TestAgySessionPTYObservationAndSavedRestore(t *testing.T) {
	teststate.Lock(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "agy-pty")
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.Symlink(exe, agy); err != nil {
		t.Fatal(err)
	}
	cwd := s.app.State().Workspaces[0].Cwd
	pane, err := s.sup.SpawnWith(1, cwd, 80, 24, []string{"HOME=" + home, "AGY_SESSION_PTY_HELPER=1", "AGY_SESSION_HOME=" + home}, []string{agy, "-test.run=^TestAgySessionPTYHelper$", "--", "--conversation", agyTestA})
	if err != nil {
		t.Fatal(err)
	}
	pane.SetAgent("codex")
	s.app.RecordSession(1, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "old-codex", Source: "report:test"})
	done := make(chan struct{})
	go func() { s.app.Run(); close(done) }()
	t.Cleanup(func() {
		s.saveMu.Lock()
		if s.saveTimer != nil {
			s.saveTimer.Stop()
		}
		s.saveMu.Unlock()
		s.sup.KillAll()
		s.app.Stop()
		<-done
	})
	wait := func(text string) {
		t.Helper()
		waitPromptCondition(t, func() bool {
			pane.PollProcess()
			return pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), text)
		})
	}
	send := func(command string) {
		t.Helper()
		if _, err := pane.WriteToPTY([]byte(command + "\n")); err != nil {
			t.Fatal(err)
		}
		wait("helper-" + command)
	}
	var prepare = func() sessionObservation {
		t.Helper()
		var o sessionObservation
		var ok bool
		s.app.Do(func() { o, ok = s.prepareSessionObservation(1) })
		if !ok {
			t.Fatal("no observation")
		}
		o.observe()
		return o
	}
	apply := func(o sessionObservation) { s.app.Do(func() { s.applySessionObservation(o) }) }
	record := func() tui.AgentRecord {
		var r tui.AgentRecord
		s.app.Do(func() { r, _ = s.app.SessionRecord(1) })
		return r
	}
	wait("helper-ready")
	// An explicit launch flag is still usable when agy holds no presence FD.
	apply(prepare())
	if r := record(); r.Agent != "agy" || r.Value != agyTestA {
		t.Fatalf("launch record %+v", r)
	}
	send("first")
	ofirst := prepare()
	apply(ofirst)
	if r := record(); r.Value != agyTestA || r.Source != "observed:file" {
		t.Fatalf("first %+v", r)
	}
	// An output generation change while an observation is in flight rejects it.
	send("again")
	stale := prepare()
	send("second")
	apply(stale)
	if r := record(); r.Value != agyTestA {
		t.Fatalf("stale observation applied %+v", r)
	}
	apply(prepare())
	if r := record(); r.Value != agyTestB || r.Source != "observed:file" {
		t.Fatalf("second %+v", r)
	}
	send("none")
	apply(prepare())
	if r := record(); r.Value != agyTestB {
		t.Fatalf("lost confirmed session %+v", r)
	}
	var snap *sessionSnapshot
	s.app.Do(func() { snap = s.captureSessionWithoutObservation() })
	if !s.writeSession(snap) {
		t.Fatal("save failed")
	}
	f, err := session.Load(session.Path(s.dir))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	f.EachPane(func(_ *session.Workspace, _ *session.Tab, p *session.Pane) {
		if p.ID == 1 {
			found = true
			if p.Agent != "agy" || p.SessionID != agyTestB {
				t.Errorf("saved %+v", p)
			}
		}
	})
	if !found {
		t.Fatal("pane missing")
	}
	cmd, ok := s.buildResumeCommand("agy", "id", agyTestB)
	if !ok || cmd != "agy --conversation "+agyTestB {
		t.Fatalf("restore %q %v", cmd, ok)
	}
	// Multiple current FDs must never select the original command-line ID.
	send("both")
	o := prepare()
	if o.found && o.result.Value == agyTestA {
		t.Fatalf("ambiguous selected launch ID: %+v", o.result)
	}
}

func TestAgyRestoreAfterReturningToShellWithOldReport(t *testing.T) {
	teststate.Lock(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "agy-shell")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.Symlink(exe, agy); err != nil {
		t.Fatal(err)
	}
	pane, err := s.sup.SpawnWith(1, s.app.State().Workspaces[0].Cwd, 80, 24, []string{"HOME=" + home, "AGY_SESSION_PTY_HELPER=1", "AGY_SESSION_HOME=" + home}, []string{"/bin/sh", "-i"})
	if err != nil {
		t.Fatal(err)
	}
	pane.SetAgent("codex")
	s.app.RecordSession(1, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "old-codex", Source: "report:test"})
	done := make(chan struct{})
	go func() { s.app.Run(); close(done) }()
	t.Cleanup(func() {
		s.saveMu.Lock()
		if s.saveTimer != nil {
			s.saveTimer.Stop()
		}
		s.saveMu.Unlock()
		s.sup.KillAll()
		s.app.Stop()
		<-done
	})
	write := func(v string) {
		t.Helper()
		if _, err := pane.WriteToPTY([]byte(v + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(fn func() bool) {
		t.Helper()
		waitPromptCondition(t, func() bool { pane.PollProcess(); return fn() })
	}
	write("'" + agy + "' -test.run=^TestAgySessionPTYHelper$")
	wait(func() bool {
		return pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), "helper-ready")
	})
	write("first")
	wait(func() bool { return strings.Contains(pane.ScreenText(), "helper-first") })
	var o sessionObservation
	var ok bool
	s.app.Do(func() { o, ok = s.prepareSessionObservation(1) })
	if !ok {
		t.Fatal("missing agy observation")
	}
	o.observe()
	s.app.Do(func() { s.applySessionObservation(o) })
	var rec tui.AgentRecord
	s.app.Do(func() { rec, _ = s.app.SessionRecord(1) })
	if rec.Agent != "agy" || rec.Value != agyTestA {
		t.Fatalf("session %+v", rec)
	}
	write("quit")
	wait(func() bool { return pane.Snapshot().ForegroundAgent == "" })
	var result api.AgentRestoreResult
	s.app.Do(func() { result = s.runAgentRestore(true) })
	if len(result.Actions) != 1 || result.Actions[0].Agent != "agy" || result.Actions[0].Status != "would_launch" || result.Actions[0].Command == nil || *result.Actions[0].Command != "agy --conversation "+agyTestA {
		t.Fatalf("restore after shell %+v", result.Actions)
	}
	oldGroup := o.processGroup
	write("'" + agy + "' -test.run=^TestAgySessionPTYHelper$ -- --conversation " + agyTestB)
	wait(func() bool {
		return pane.Snapshot().ForegroundAgent == "agy" && pane.ForegroundJob().ProcessGroupID != oldGroup && strings.Count(pane.ScreenText(), "helper-ready") >= 2
	})
	s.app.Do(func() { o, ok = s.prepareSessionObservation(1) })
	if !ok {
		t.Fatal("missing second process observation")
	}
	o.observe()
	s.app.Do(func() { s.applySessionObservation(o) })
	s.app.Do(func() { rec, _ = s.app.SessionRecord(1) })
	if rec.Agent != "agy" || rec.Value != agyTestB || rec.Source != "ikesu:antigravity_cli" {
		t.Fatalf("second process launch was ignored: %+v", rec)
	}
}

func TestAgyDelayedPresenceSavedAfterPTYOutput(t *testing.T) {
	teststate.Lock(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "agy-delayed-save")
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.Symlink(exe, agy); err != nil {
		t.Fatal(err)
	}
	pane, err := s.sup.SpawnWith(1, s.app.State().Workspaces[0].Cwd, 80, 24, []string{"HOME=" + home, "AGY_SESSION_PTY_HELPER=1", "AGY_SESSION_HOME=" + home}, []string{agy, "-test.run=^TestAgySessionPTYHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	s.outEmit = map[int]*outputEmit{}
	s.app.SetChangeHook(s.markSessionDirty)
	s.app.SetOutputHook(s.onPaneOutput)
	s.sup.IdleActivityChanged = s.markSessionDirty
	done := make(chan struct{})
	go func() { s.app.Run(); close(done) }()
	t.Cleanup(func() {
		s.saveMu.Lock()
		if s.saveTimer != nil {
			s.saveTimer.Stop()
		}
		s.saveMu.Unlock()
		s.sup.KillAll()
		s.app.Stop()
		<-done
	})
	waitPromptCondition(t, func() bool {
		pane.PollProcess()
		return pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), "helper-ready")
	})
	pane.RecordKeyInput()
	if _, err := pane.WriteToPTY([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	waitPromptCondition(t, func() bool {
		f, err := session.Load(session.Path(s.dir))
		if err != nil || f == nil {
			return false
		}
		found := false
		f.EachPane(func(_ *session.Workspace, _ *session.Tab, p *session.Pane) {
			if p.ID == 1 && p.Agent == "agy" && p.SessionID == agyTestA {
				found = true
			}
		})
		return found
	})
}

func TestAgyFailedObservationRetriesOnceWithoutRebinding(t *testing.T) {
	teststate.Lock(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, "agy-retry")
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agy := filepath.Join(t.TempDir(), "agy")
	if err := os.Symlink(exe, agy); err != nil {
		t.Fatal(err)
	}
	pane, err := s.sup.SpawnWith(1, s.app.State().Workspaces[0].Cwd, 80, 24, []string{"HOME=relative", "AGY_SESSION_PTY_HELPER=1", "AGY_SESSION_HOME=" + home}, []string{agy, "-test.run=^TestAgySessionPTYHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	s.app.RecordSession(1, tui.AgentRecord{Agent: "agy", Kind: "id", Value: agyTestA, Source: "observed:file"})
	done := make(chan struct{})
	go func() { s.app.Run(); close(done) }()
	t.Cleanup(func() {
		s.saveMu.Lock()
		if s.saveTimer != nil {
			s.saveTimer.Stop()
		}
		s.saveMu.Unlock()
		s.sup.KillAll()
		s.app.Stop()
		<-done
	})
	waitPromptCondition(t, func() bool {
		pane.PollProcess()
		return pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), "helper-ready")
	})
	oldStart := time.Unix(42, 0)
	s.app.Do(func() {
		s.observedStart = map[int]time.Time{1: oldStart}
		s.agyObservedGroup = map[int]int{1: 12345}
		s.markSessionDirty()
	})
	waitPromptCondition(t, func() bool {
		var exhausted bool
		s.app.Do(func() { entry, ok := s.agyScans[1]; exhausted = ok && !entry.retry.MayScan() })
		return exhausted
	})
	s.app.Do(func() {
		rec, _ := s.app.SessionRecord(1)
		if rec.Value != agyTestA || !s.observedStart[1].Equal(oldStart) || s.agyObservedGroup[1] != 12345 {
			t.Errorf("failed read changed previous identity: %+v start=%v group=%d", rec, s.observedStart[1], s.agyObservedGroup[1])
		}
		if _, ok := s.prepareSessionObservation(1); ok {
			t.Error("failed observation was not capped")
		}
		pane.RecordKeyInput()
		if _, ok := s.prepareSessionObservation(1); !ok {
			t.Error("new input did not reopen observation")
		}
	})
	waitPromptCondition(t, func() bool { s.saveMu.Lock(); defer s.saveMu.Unlock(); return !s.saveRunning && !s.savePending })
}
