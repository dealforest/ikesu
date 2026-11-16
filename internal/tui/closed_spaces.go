package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/session"
)

// Closed spaces: when a whole space goes away with its panes (closed from
// a menu, the last tab or pane closed, or the last shell exited), the app
// hands its layout to the server, which keeps the most recent ones. The
// main menu lists them and reopens one as a new space: same name, tabs,
// splits and folders, and AI panes resume their recorded conversations.
// Spaces emptied by moving their panes elsewhere are not recorded: the
// panes still exist.

// ClosedSpace is one entry of the main menu's reopen list.
type ClosedSpace struct {
	ID       string
	Label    string
	Reason   string // "closed" | "exited"
	ClosedAt time.Time
	Panes    int
	Agents   []string
}

const menuReopenSpace = "reopen closed space…"

// SetClosedSpaceHooks registers how closed spaces are recorded, listed and
// reopened. record runs on the app loop before the space's panes are
// killed, so their processes and session records are still readable.
// reopen returns the reopened space's index.
func (a *App) SetClosedSpaceHooks(record func(ws session.Workspace, label, reason string), list func() []ClosedSpace, reopen func(id string) (int, error)) {
	a.spaceClosedHook, a.closedSpacesList, a.closedSpaceReopen = record, list, reopen
}

// closingSpace is the space a close command may remove, captured before
// the command runs.
type closingSpace struct {
	ws     *model.Workspace
	saved  session.Workspace // taken before the command: closing a tab empties ws.Tabs
	label  string
	reason string
}

// execWithoutClosedRecord runs a close that undoes a space this app just
// created after a failure; such a space is not offered for reopening.
func (a *App) execWithoutClosedRecord(cmd model.Command) {
	was := a.skipClosedRecord
	a.skipClosedRecord = true
	defer func() { a.skipClosedRecord = was }()
	a.exec(cmd)
}

func (a *App) closingSpaceFor(cmd model.Command) *closingSpace {
	if a.spaceClosedHook == nil || a.skipClosedRecord {
		return nil
	}
	wi, reason := -1, "closed"
	lastPane := func(pane model.PaneID) int {
		i, _ := a.st.FindPane(pane)
		if i < 0 || len(a.st.Workspaces[i].Tabs) != 1 || a.st.Workspaces[i].Tabs[0].Layout.PaneCount() != 1 {
			return -1
		}
		return i
	}
	switch c := cmd.(type) {
	case model.CmdCloseWorkspace:
		wi = c.Index
	case model.CmdCloseTab:
		if c.Workspace >= 0 && c.Workspace < len(a.st.Workspaces) && len(a.st.Workspaces[c.Workspace].Tabs) == 1 {
			wi = c.Workspace
		}
	case model.CmdClosePane:
		wi = lastPane(c.Pane)
	case model.CmdPaneExited:
		wi = lastPane(c.Pane)
		reason = "exited"
	}
	if wi < 0 || wi >= len(a.st.Workspaces) {
		return nil
	}
	ws := a.st.Workspaces[wi]
	return &closingSpace{ws: ws, saved: a.st.SnapshotWorkspace(ws), label: a.WorkspaceLabel(wi), reason: reason}
}

// recordClosedSpace reports the captured space when the command removed it
// and killed every one of its panes. Call before the effects run.
func (a *App) recordClosedSpace(c *closingSpace, effects []model.Effect) {
	if c == nil {
		return
	}
	for _, ws := range a.st.Workspaces {
		if ws == c.ws {
			return
		}
	}
	killed := map[int]bool{}
	for _, e := range effects {
		if k, ok := e.(model.EffKillPane); ok {
			killed[k.Pane] = true
		}
	}
	for _, t := range c.saved.Tabs {
		for _, p := range t.Panes {
			if !killed[p.ID] {
				return
			}
		}
	}
	a.spaceClosedHook(c.saved, c.label, c.reason)
}

// openReopenSpaceMenu lists the recently closed spaces, newest first.
func (a *App) openReopenSpaceMenu(x, y int) {
	if a.closedSpacesList == nil || a.closedSpaceReopen == nil {
		return
	}
	spaces := a.closedSpacesList()
	if len(spaces) == 0 {
		a.ShowToast("reopen space", "no closed space to reopen", "info", a.focusedPane())
		return
	}
	now := time.Now()
	labels := make([]string, 0, len(spaces))
	actions := make([]func(), 0, len(spaces))
	for _, sp := range spaces {
		id := sp.ID
		labels = append(labels, closedSpaceLine(sp, now))
		actions = append(actions, func() {
			index, err := a.closedSpaceReopen(id)
			if err != nil {
				a.ShowToast("reopen space failed", err.Error(), "error", a.focusedPane())
				return
			}
			a.selectWorkspace(index)
		})
	}
	a.openMoveMenu(x, y, "Reopen closed space", labels, actions)
}

// closedSpaceLine renders one menu row: name, pane count, AI kinds, age.
func closedSpaceLine(sp ClosedSpace, now time.Time) string {
	parts := []string{sp.Label, fmt.Sprintf("%d pane%s", sp.Panes, map[bool]string{true: "", false: "s"}[sp.Panes == 1])}
	if len(sp.Agents) > 0 {
		parts = append(parts, strings.Join(sp.Agents, ","))
	}
	if sp.Reason == "exited" {
		parts = append(parts, "shell exited")
	}
	parts = append(parts, closedAge(now.Sub(sp.ClosedAt)))
	return strings.Join(parts, " · ")
}

func closedAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
