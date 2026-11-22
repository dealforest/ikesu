package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/teststate"
)

func TestLocalAgyFDHelper(t *testing.T) {
	if os.Getenv("LOCAL_AGY_FD_HELPER") != "1" {
		return
	}
	path := filepath.Join(os.Getenv("LOCAL_AGY_TEST_HOME"), ".gemini", "antigravity-cli", "presence", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		os.Exit(2)
	}
	if os.Getenv("LOCAL_AGY_NO_FD") != "1" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			os.Exit(3)
		}
		defer f.Close()
	}
	fmt.Println("local-agy-ready")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}

func TestLocalAgySessionObservedOffLoop(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
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
	pane, err := a.sup.SpawnWith(1, home, 80, 24, []string{"HOME=" + home, "LOCAL_AGY_FD_HELPER=1", "LOCAL_AGY_TEST_HOME=" + home}, []string{agy, "-test.run=^TestLocalAgyFDHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	pane.SetAgent("codex")
	a.RecordSession(1, AgentRecord{Agent: "codex", Kind: "id", Value: "old", Source: "report:test"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pane.PollProcess()
		if pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), "local-agy-ready") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pane.Snapshot().ForegroundAgent != "agy" {
		t.Fatal("agy not running")
	}
	for time.Now().Before(deadline) {
		a.observeLocalSessions()
		rec, _ := a.SessionRecord(1)
		if rec.Agent == "agy" && rec.Value == "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" && rec.Source == "observed:file" {
			a.observeLocalSessions()
			if a.localAgyPending[1] {
				t.Fatal("repeated scan for unchanged output")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	rec, _ := a.SessionRecord(1)
	t.Fatalf("local session not observed: %+v", rec)
}

func TestLocalAgyNewProcessAndShellRestore(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
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
	pane, err := a.sup.SpawnWith(1, home, 80, 24, []string{"HOME=" + home, "LOCAL_AGY_FD_HELPER=1", "LOCAL_AGY_TEST_HOME=" + home}, []string{"/bin/sh", "-i"})
	if err != nil {
		t.Fatal(err)
	}
	pane.SetAgent("codex")
	a.RecordSession(1, AgentRecord{Agent: "codex", Kind: "id", Value: "old-codex", Source: "report:test"})
	write := func(text string) {
		t.Helper()
		if _, err := pane.WriteToPTY([]byte(text + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			pane.PollProcess()
			a.observeLocalSessions()
			if check() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("PTY condition not reached")
	}
	write("'" + agy + "' -test.run=^TestLocalAgyFDHelper$")
	wait(func() bool {
		r, _ := a.SessionRecord(1)
		return r.Agent == "agy" && r.Value == "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	})
	previousGroup := a.localAgyGroup[1]
	write("")
	wait(func() bool { return pane.Snapshot().ForegroundAgent == "" && pane.Snapshot().FgPID == pane.ShellPID() })
	a.restoreLocalAgents(true)
	if a.toast == nil || a.toast.body != "would launch 1, skipped 0" {
		t.Fatalf("local shell restore: %+v", a.toast)
	}
	write("LOCAL_AGY_NO_FD=1 '" + agy + "' -test.run=^TestLocalAgyFDHelper$ -- --conversation bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	wait(func() bool {
		r, _ := a.SessionRecord(1)
		return a.localAgyGroup[1] != previousGroup && r.Value == "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb" && r.Source == "ikesu:antigravity_cli"
	})
}

func TestLocalAgyFailedObservationRetriesOnceWithoutRebinding(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
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
	pane, err := a.sup.SpawnWith(1, home, 80, 24, []string{"HOME=relative", "LOCAL_AGY_FD_HELPER=1", "LOCAL_AGY_TEST_HOME=" + home}, []string{agy, "-test.run=^TestLocalAgyFDHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	a.RecordSession(1, AgentRecord{Agent: "agy", Kind: "id", Value: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Source: "observed:file"})
	oldStart := time.Unix(42, 0)
	a.localObservedStart = map[int]time.Time{1: oldStart}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pane.PollProcess()
		if pane.Snapshot().ForegroundAgent == "agy" && strings.Contains(pane.ScreenText(), "local-agy-ready") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	a.observeLocalSessions() // initializes the local scan state and starts the first attempt
	a.localAgyGroup[1] = 12345
	for time.Now().Before(deadline) {
		a.observeLocalSessions()
		entry, ok := a.localAgySeen[1]
		if ok && !entry.retry.MayScan() && !a.localAgyPending[1] {
			break
		}
		time.Sleep(time.Millisecond)
	}
	entry, ok := a.localAgySeen[1]
	if !ok || entry.retry.MayScan() {
		t.Fatal("failed scan did not stop after retry")
	}
	rec, _ := a.SessionRecord(1)
	if rec.Value != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" || !a.localObservedStart[1].Equal(oldStart) || a.localAgyGroup[1] != 12345 {
		t.Fatalf("failed scan rebound previous identity: %+v", rec)
	}
	a.observeLocalSessions()
	if a.localAgyPending[1] {
		t.Fatal("unchanged failed scan restarted")
	}
	pane.RecordKeyInput()
	a.observeLocalSessions()
	if !a.localAgyPending[1] {
		t.Fatal("new input did not reopen observation")
	}
}
