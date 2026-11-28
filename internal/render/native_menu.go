package render

import (
	"encoding/base64"
	"encoding/json"
)

// NativeMenuOSC is the private OSC number that carries the open menu or
// dialog to a host app ([ui] native_menus): ESC ] 7767 ; ikesu-ui ;
// <base64(JSON)> BEL. The host draws its own list or dialog and answers
// with the ordinary keys (up/down, enter, esc), so key handling is shared
// with the terminal-drawn menu.
const NativeMenuOSC = "7767"

type nativeMenuItem struct {
	Label     string `json:"label"`
	Detail    string `json:"detail"`
	Status    string `json:"status"`
	Disabled  bool   `json:"disabled"`
	Separator bool   `json:"separator"`
}

type nativeMenuPayload struct {
	Kind       string           `json:"kind"`
	Title      string           `json:"title"`
	Sel        int              `json:"sel"`
	Items      []nativeMenuItem `json:"items"`
	Filterable bool             `json:"filterable"`
}

type nativeDialogPayload struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Detail       string   `json:"detail"`
	Lines        []string `json:"lines"`
	Confirm      string   `json:"confirm"`
	Cancel       string   `json:"cancel"`
	Input        *string  `json:"input"`
	ContinueOnly bool     `json:"continue_only"`
	NoButtons    bool     `json:"no_buttons,omitempty"` // progress only: no buttons, no dismiss
}

const nativeNoneJSON = `{"kind":"none"}`

// NativeMenuJSON is the one-line JSON for what is open. A dialog wins over
// a menu because it is drawn on top of it.
func NativeMenuJSON(menu *Menu, dialog *Dialog) string {
	var v any
	switch {
	case dialog != nil:
		lines := dialog.Lines
		if lines == nil {
			lines = []string{}
		}
		p := nativeDialogPayload{Kind: "dialog", Title: dialog.Title, Detail: dialog.Detail, Lines: lines,
			Confirm: dialog.Confirm, Cancel: dialog.Cancel, ContinueOnly: dialog.ContinueOnly, NoButtons: dialog.NoButtons}
		if dialog.InputEdit || dialog.WorktreeCreate {
			text := dialog.Input
			p.Input = &text
		}
		if dialog.WorktreeCreate && dialog.Checkout != "" {
			p.Lines = append(append([]string{}, lines...), dialog.Checkout)
		}
		v = p
	case menu != nil:
		p := nativeMenuPayload{Kind: "menu", Title: menu.Title, Sel: menu.Sel, Items: make([]nativeMenuItem, 0, len(menu.Items)), Filterable: menu.Worktree}
		if p.Sel < 0 || p.Sel >= len(menu.Items) {
			p.Sel = -1
		}
		for _, it := range menu.Items {
			p.Items = append(p.Items, nativeMenuItem{Label: it.Label, Detail: it.Detail, Status: it.Status, Disabled: it.Disabled, Separator: it.Separator})
		}
		v = p
	default:
		return nativeNoneJSON
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nativeNoneJSON
	}
	return string(data)
}

// NativeMenuSequence wraps the JSON in the OSC that a host app reads.
func NativeMenuSequence(payload string) string {
	return "\x1b]" + NativeMenuOSC + ";ikesu-ui;" + base64.StdEncoding.EncodeToString([]byte(payload)) + "\x07"
}

// NativeMenuSignal remembers what one output last announced, so each
// change is written once. The zero value means "nothing open" was last
// announced: a fresh output with nothing open writes nothing.
type NativeMenuSignal struct {
	last string
}

// Next returns the bytes to append to this output's frame, or nil. When
// enabled is false nothing is written, except one closing "none" if an
// earlier enabled frame announced something still open.
func (s *NativeMenuSignal) Next(enabled bool, menu *Menu, dialog *Dialog) []byte {
	current := nativeNoneJSON
	if enabled {
		current = NativeMenuJSON(menu, dialog)
	}
	last := s.last
	if last == "" {
		last = nativeNoneJSON
	}
	if current == last {
		return nil
	}
	s.last = current
	return []byte(NativeMenuSequence(current))
}

// Forget makes the next Next call treat the output as freshly connected
// with nothing announced (e.g. after the frame write failed).
func (s *NativeMenuSignal) Forget() { s.last = "" }
