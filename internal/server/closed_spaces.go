package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"crypto/rand"

	"github.com/kazuph/ikesu/internal/session"
	"github.com/kazuph/ikesu/internal/tui"
)

// Recently closed spaces, kept so a space closed by mistake can be
// reopened from the main menu. The list lives in closed_spaces.json next
// to session.json, newest first, at most closedSpacesMax entries. It is
// read and changed only on the app loop; the file is written off the loop.

const closedSpacesMax = 20

type closedSpaceEntry struct {
	ID        string            `json:"id"`
	ClosedAt  time.Time         `json:"closed_at"`
	Reason    string            `json:"reason"`
	Label     string            `json:"label"`
	Workspace session.Workspace `json:"workspace"`
}

type closedSpaceStore struct {
	loaded  bool
	entries []closedSpaceEntry
	seq     uint64

	writeMu  sync.Mutex
	writeSeq uint64
}

func closedSpacesPath(dir string) string { return filepath.Join(dir, "closed_spaces.json") }

func (s *Server) closedSpaceEntries() []closedSpaceEntry {
	c := &s.closedSpaces
	if !c.loaded {
		c.loaded = true
		if data, err := os.ReadFile(closedSpacesPath(s.dir)); err == nil {
			if err := json.Unmarshal(data, &c.entries); err != nil {
				fmt.Fprintf(os.Stderr, "ikesu: ignoring closed spaces list: %v\n", err)
				c.entries = nil
			}
		}
	}
	return c.entries
}

// recordClosedSpace runs on the app loop before the space's panes die.
func (s *Server) recordClosedSpace(ws session.Workspace, label, reason string) {
	pane := 0
	for ti := range ws.Tabs {
		for pi := range ws.Tabs[ti].Panes {
			s.observePaneSession(ws.Tabs[ti].Panes[pi].ID)
			pane++
		}
	}
	if pane == 0 {
		return
	}
	fill := s.paneRecordFiller()
	for ti := range ws.Tabs {
		for pi := range ws.Tabs[ti].Panes {
			fill(&ws, &ws.Tabs[ti].Panes[pi])
		}
	}
	entry := closedSpaceEntry{ID: rand.Text(), ClosedAt: time.Now(), Reason: reason, Label: label, Workspace: ws}
	entries := append([]closedSpaceEntry{entry}, s.closedSpaceEntries()...)
	if len(entries) > closedSpacesMax {
		entries = entries[:closedSpacesMax]
	}
	s.closedSpaces.entries = entries
	s.persistClosedSpaces()
}

func (s *Server) listClosedSpaces() []tui.ClosedSpace {
	var out []tui.ClosedSpace
	for _, e := range s.closedSpaceEntries() {
		item := tui.ClosedSpace{ID: e.ID, Label: e.Label, Reason: e.Reason, ClosedAt: e.ClosedAt}
		seen := map[string]bool{}
		for _, t := range e.Workspace.Tabs {
			for _, p := range t.Panes {
				item.Panes++
				if p.Agent != "" && !seen[p.Agent] {
					seen[p.Agent] = true
					item.Agents = append(item.Agents, p.Agent)
				}
			}
		}
		out = append(out, item)
	}
	return out
}

// reopenClosedSpace appends the saved space as a new space and types the
// resume command into each AI pane with a recorded session. It returns the
// new space's index. The entry is removed only after the space exists.
func (s *Server) reopenClosedSpace(id string) (int, error) {
	entries := s.closedSpaceEntries()
	at := -1
	for i, e := range entries {
		if e.ID == id {
			at = i
			break
		}
	}
	if at < 0 {
		return 0, errors.New("the space is no longer in the closed list")
	}
	// Deep copy: Append and the cwd fallbacks must not touch the stored entry.
	raw, err := json.Marshal(entries[at].Workspace)
	if err != nil {
		return 0, err
	}
	var ws session.Workspace
	if err := json.Unmarshal(raw, &ws); err != nil {
		return 0, err
	}
	if !dirIsPresent(ws.Cwd) {
		return 0, fmt.Errorf("the space's folder no longer exists: %s", ws.Cwd)
	}
	for ti := range ws.Tabs {
		for pi := range ws.Tabs[ti].Panes {
			if p := &ws.Tabs[ti].Panes[pi]; !dirIsPresent(p.Cwd) {
				p.Cwd = ws.Cwd
			}
		}
	}
	prev := s.app.State().Capture()
	reserved, dbErr := s.reservedPaneIDs()
	state := s.app.State()
	target := state.Snapshot()
	target.NextPaneID = max(target.NextPaneID, state.HandoffCounters().RuntimePane)
	// Append adopts the added file's sidebar settings; keep the current ones.
	file := &session.File{Version: session.CurrentVersion, Workspaces: []session.Workspace{ws},
		SidebarWidth: target.SidebarWidth, SidebarManual: target.SidebarManual, SidebarVisible: target.SidebarVisible}
	merged, added := session.Append(target, file, reserved, dbErr != nil)
	if err := s.app.AppendSession(merged, added); err != nil {
		return 0, err
	}
	s.afterExec(nil, prev)
	added.EachPane(func(_ *session.Workspace, _ *session.Tab, p *session.Pane) {
		if p.Agent == "" {
			return
		}
		if cmd, ok := s.buildResumeCommand(p.Agent, p.SessionKind, p.SessionID); ok {
			s.typeResume(p.ID, cmd)
		}
	})
	s.closedSpaces.entries = append(append([]closedSpaceEntry(nil), entries[:at]...), entries[at+1:]...)
	s.persistClosedSpaces()
	s.saveSession()
	return len(s.app.State().Workspaces) - 1, nil
}

func dirIsPresent(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// persistClosedSpaces writes the list off the app loop; a slower older
// write never replaces a newer one.
func (s *Server) persistClosedSpaces() {
	c := &s.closedSpaces
	data, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ikesu: closed spaces: %v\n", err)
		return
	}
	c.seq++
	seq := c.seq
	path := closedSpacesPath(s.dir)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		if seq <= c.writeSeq {
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "ikesu: closed spaces: %v\n", err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			fmt.Fprintf(os.Stderr, "ikesu: closed spaces: %v\n", err)
			return
		}
		c.writeSeq = seq
	}()
}
