package runtime

import (
	"github.com/kazuph/ikesu/internal/guard"
	"github.com/kazuph/ikesu/internal/teststate"
	"os"
	"path/filepath"
	"testing"
)

// 2つのセッションが同じペイン番号を使っても、guard の作業フォルダを奪い合わない（Issue #67）。
func TestGuardFoldersDoNotCollideAcrossSessions(t *testing.T) {
	teststate.Lock(t)
	d := t.TempDir()
	teststate.Setenv(t, "HOME", d)
	teststate.Setenv(t, "ZDOTDIR", d)
	teststate.Setenv(t, "BASH_ENV", "")
	teststate.Setenv(t, "SHELL", "/bin/sh")
	roots := []string{d, filepath.Join(d, "proof")}
	var panes []*Pane
	for _, root := range roots {
		s := NewSupervisor()
		if e := s.WaitLoginPath(); e != nil {
			t.Fatal(e)
		}
		s.SetGuard(guard.Config{Mode: "record"}, root)
		t.Cleanup(s.KillAll)
		p, e := s.SpawnAgent(1, d, 80, 24, nil, []string{"/bin/sleep", "60"})
		if e != nil {
			t.Fatal(e)
		}
		panes = append(panes, p)
	}
	if panes[0].GuardMarkerPath() == panes[1].GuardMarkerPath() || panes[0].GuardRoot() == panes[1].GuardRoot() {
		t.Fatalf("sessions share guard folders: %q %q", panes[0].GuardMarkerPath(), panes[1].GuardMarkerPath())
	}
	for i, root := range roots {
		if want := filepath.Join(root, "guard", "agent-panes", "p_1.json"); panes[i].GuardMarkerPath() != want {
			t.Fatalf("marker %d = %q, want %q", i, panes[i].GuardMarkerPath(), want)
		}
		if _, e := os.Stat(panes[i].GuardMarkerPath()); e != nil {
			t.Fatalf("marker %d missing: %v", i, e)
		}
	}
}
