package client

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
)

// Clicks every target on this machine in both modes using two real servers and PTYs.
func TestSidebarContinuousTwoServersRightClicksLocal(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	sidebarContinuousTwoServersRightClicks(t, "local")
}

// Clicks every target on this machine in both modes using two real servers and PTYs.
func TestSidebarContinuousTwoServersRightClicksRemote(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	sidebarContinuousTwoServersRightClicks(t, "remote")
}

func sidebarContinuousTwoServersRightClicks(t *testing.T, targetMachine string) {
	h := newRightMachineHarness(t)
	m := h.m
	menus := map[string][]render.MenuItem{}
	checked := 0
	switchSource := func(source string) {
		if m.active == source {
			return
		}
		m.pending = &pendingMachine{id: source, workspace: m.endpoints[source].snapshot.State.ActiveWorkspace().ID, deadline: time.Now().Add(machineSwitchWait)}
		m.requestPending()
		h.until("source machine visible", func() bool { return m.pending == nil && m.active == source })
	}
	// Follow the original click order across both modes, including the other machine's hits.
	previousMachine := "local"
	for _, compact := range []int{0, 1} {
		switchSource(previousMachine)
		h.modes(compact, compact, 240, 60)
		hits := append([]render.MachineHit(nil), m.sidebar.Hits...)
		for _, hit := range hits {
			if hit.Header || hit.New || hit.Right && hit.Row.Kind != render.RowAgent || !hit.Right && hit.Row.Kind != render.RowWorkspace && hit.Row.Kind != render.RowGit && hit.Row.Kind != render.RowCardBlank {
				continue
			}
			source := previousMachine
			previousMachine = hit.Machine
			if hit.Machine != targetMachine {
				continue
			}
			switchSource(source)
			actualSource := m.active
			if err := m.input(mouseEvent(t, fmt.Sprintf("\x1b[<2;%d;%dM", hit.Rect.X+1, hit.Rect.Y+1))); err != nil {
				t.Fatal(err)
			}
			e := m.endpoints[hit.Machine]
			h.until(fmt.Sprintf("right-click compact=%d machine=%s right=%t row=%+v menu=%t", compact, hit.Machine, hit.Right, hit.Row, e.snapshot.Menu != nil), func() bool {
				return m.pending == nil && m.active == hit.Machine && e.snapshot.Menu != nil
			})
			key := fmt.Sprintf("%s/%t/%+v", hit.Machine, hit.Right, hit.Row)
			if compact == 0 {
				menus[key] = append([]render.MenuItem(nil), e.snapshot.Menu.Items...)
			} else if !reflect.DeepEqual(menus[key], e.snapshot.Menu.Items) {
				t.Fatalf("compact menu changed actions for %s", key)
			}
			var owner string
			if hit.Right {
				if e.snapshot.State.FocusedPane() != hit.Row.Pane {
					t.Fatal("pane menu targeted another pane", key)
				}
				owner = fmt.Sprintf("pane:%d", hit.Row.Pane)
			} else {
				// Open Rename to observe the menu's workspace owner, then cancel.
				steps, found := 0, false
				for _, item := range e.snapshot.Menu.Items {
					if item.Label == "Rename" {
						found = true
						break
					}
					if !item.Separator && !item.Disabled {
						steps++
					}
				}
				if !found {
					t.Fatal("workspace menu missing Rename")
				}
				if err := m.sendInput(hit.Machine, []byte(strings.Repeat("\x1b[B", steps)+"\r")); err != nil {
					t.Fatal(err)
				}
				h.until("workspace menu owner", func() bool {
					d := e.snapshot.Dialog
					return d != nil && d.Title == "Rename workspace" && d.Input == render.WorkspaceDisplayName(e.snapshot.State.Workspaces[hit.Row.Ws], e.snapshot.Panes)
				})
				workspace := e.snapshot.State.Workspaces[hit.Row.Ws]
				owner = fmt.Sprintf("workspace:%d:%q", workspace.ID, render.WorkspaceDisplayName(workspace, e.snapshot.Panes))
			}
			t.Logf("compact=%d source=%s machine=%s right=%t row=%+v x=%d y=%d owner=%s", compact, actualSource, hit.Machine, hit.Right, hit.Row, hit.Rect.X, hit.Rect.Y, owner)
			if err := m.sendInput(hit.Machine, []byte("\x1b[27u")); err != nil {
				t.Fatal(err)
			}
			h.until("context overlay closed", func() bool { return e.snapshot.Menu == nil && e.snapshot.Dialog == nil })
			m.suppressRelease = false
			checked++
		}
	}
	if checked != 2*len(menus) || checked == 0 {
		t.Fatal("expanded/compact target counts differ", checked, len(menus))
	}
	t.Logf("PASS %d real right-click targets at widths26/2, machine=%s, same menus and observed workspace/pane owners", checked, targetMachine)
}
