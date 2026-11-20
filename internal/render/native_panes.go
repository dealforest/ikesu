package render

import (
	"encoding/json"

	"github.com/kazuph/ikesu/internal/model"
)

type nativePane struct {
	ID int `json:"id"`
	X  int `json:"x"`
	Y  int `json:"y"`
	W  int `json:"w"`
	H  int `json:"h"`
}
type nativePanesPayload struct {
	Kind   string       `json:"kind"`
	Cols   int          `json:"cols"`
	Rows   int          `json:"rows"`
	Panes  []nativePane `json:"panes"`
	Spaces bool         `json:"spaces"`
}

// NativePaneSignal belongs to one output stream. It only announces complete,
// visible content rectangles, never clipped geometry or covered pane content.
type NativePaneSignal struct {
	last            string
	blocked, spaces bool
}

func (s *NativePaneSignal) Next(enabled bool, in Input, cols, rows int, covered bool) []byte {
	if !enabled {
		s.Forget()
		return nil
	}
	state := in.State
	blocked := covered || state == nil || in.Menu != nil || in.Dialog != nil || in.Help != nil ||
		in.Settings != nil || in.Navigator != nil || in.Approval != nil || in.Toast != nil ||
		in.Copy != nil || in.ConfigWarning != "" || in.Copied != "" || in.ModeHint != "" ||
		(state != nil && (state.PopupVisible() || (state.Mobile() && in.MobileSwitcher)))
	spaces := !covered && state != nil && in.Menu == nil && in.Dialog == nil &&
		((state.Mobile() && in.MobileSwitcher) || (!state.Mobile() && state.SidebarVisible && state.SidebarArea().W > 0))
	if blocked && s.blocked && s.last != "" && s.spaces == spaces {
		return nil
	}
	p := nativePanesPayload{Kind: "panes", Cols: cols, Rows: rows, Panes: []nativePane{}, Spaces: spaces}
	if !blocked {
		for _, pr := range state.DisplayPaneRects() {
			r := pr.Rect
			if r.X < 0 || r.Y < 0 || r.W <= 0 || r.H <= 0 || r.X+r.W > cols || r.Y+r.H > rows {
				continue
			}
			inner, _ := model.PaneContentRect(r, state.PaneBorders)
			if inner.W <= 0 || inner.H <= 0 {
				continue
			}
			p.Panes = append(p.Panes, nativePane{ID: pr.ID, X: inner.X, Y: inner.Y, W: inner.W, H: inner.H})
		}
	}
	encoded, _ := json.Marshal(p)
	current := string(encoded)
	s.blocked, s.spaces = blocked, spaces
	if current == s.last {
		return nil
	}
	s.last = current
	return []byte(NativeMenuSequence(current))
}
func (s *NativePaneSignal) Forget() { *s = NativePaneSignal{} }
