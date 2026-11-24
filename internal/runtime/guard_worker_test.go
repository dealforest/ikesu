package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/guard"
)

// Block a real reconfiguration before it touches assets so the next batch
// accumulates deterministically.
type gatedPaneGuard struct {
	PaneGuard
	entered chan struct{}
	release chan struct{}
	modes   []string
	agents  []string
}

func (g *gatedPaneGuard) Reconfigure(c guard.Config) error {
	if len(g.modes) == 0 {
		close(g.entered)
		<-g.release
	}
	g.modes = append(g.modes, c.EffectiveMode())
	return g.PaneGuard.Reconfigure(c)
}

func (g *gatedPaneGuard) Sync(agent string) error {
	g.agents = append(g.agents, agent)
	return g.PaneGuard.Sync(agent)
}

func TestGuardWorkerCoalescesAndFlushesAssets(t *testing.T) {
	root := t.TempDir()
	s := NewSupervisor()
	t.Cleanup(s.KillAll)
	a := guard.NewAssets(root, "p_1", "/usr/bin/true", guard.Config{})
	socket := filepath.Join(root, "s.sock")
	env := []string{"HOME=" + root, "PATH=/bin", "IKESU_PANE_ID=p1", "IKESU_SOCKET_PATH=" + socket}
	if _, err := a.Prepare(env, root); err != nil {
		t.Fatal(err)
	}
	g := &gatedPaneGuard{PaneGuard: a, entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(g.release)
		}
	}()
	p := s.newPane(1, root, 80, 24)
	p.guard = g
	s.panes[p.ID] = p

	wait := func(ch <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal(message)
		}
	}
	submit := func(f func(), message string) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			f()
			close(done)
		}()
		wait(done, message)
	}
	submit(func() { s.SetGuard(guard.Config{Mode: "record"}, root) }, "SetGuard blocked on asset I/O")
	wait(g.entered, "guard worker did not start")
	submit(func() {
		p.SetAgent("claude")
		s.SetGuard(guard.Config{Mode: "record"}, root)
		s.SetGuard(guard.Config{Mode: "enforce"}, root)
		p.SetAgent("")
		p.SetAgent("codex")
	}, "guard updates blocked while a batch was running")

	if _, err := os.Stat(a.MarkerPath); !os.IsNotExist(err) {
		t.Fatalf("marker before worker release: %v", err)
	}
	close(g.release)
	released = true
	s.FlushGuardSync()

	meta, active, err := guard.ReadMarker(guard.Input{Env: map[string]string{
		"IKESU_GUARD_MARKER": a.MarkerPath, "IKESU_PANE_ID": "p1", "IKESU_SOCKET_PATH": socket,
	}})
	if err != nil || !active || meta.Agent != "codex" || meta.Mode != "enforce" {
		t.Fatalf("flush left marker active=%v meta=%+v err=%v", active, meta, err)
	}
	if !slices.Equal(g.modes, []string{"record", "enforce"}) || !slices.Equal(g.agents, []string{"codex", "codex"}) {
		t.Fatalf("updates were not coalesced: modes=%v agents=%v", g.modes, g.agents)
	}
}
