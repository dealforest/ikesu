package tui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/teststate"
)

var nativeMenuOSC = regexp.MustCompile("\x1b\\]7767;ikesu-ui;([A-Za-z0-9+/=]*)\x07")

// nativeSignals decodes every ikesu-ui OSC in one frame's output.
func nativeSignals(t *testing.T, out []byte) []map[string]any {
	t.Helper()
	var got []map[string]any
	for _, m := range nativeMenuOSC.FindAllSubmatch(out, -1) {
		raw, err := base64.StdEncoding.DecodeString(string(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.ContainsRune(raw, '\n') {
			t.Fatalf("payload must be one line: %q", raw)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err, string(raw))
		}
		got = append(got, v)
	}
	return got
}

func nativeFrame(t *testing.T, a *App, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	buf.Reset()
	a.dirty = true
	a.draw()
	return nativeSignals(t, buf.Bytes())
}

func nativeKey(a *App, code rune, text string) {
	key := uv.Key{Code: code, Text: text}
	a.onInput(input.Event{Kind: input.KindKey, Key: key})
	a.onInput(input.Event{Kind: input.KindKeyRelease, Key: key})
}

func nativeApp(t *testing.T, enabled bool) (*App, *bytes.Buffer) {
	t.Helper()
	a := specRuntimeApp(t)
	if err := a.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	a.exec(model.CmdNewTab{Workspace: 0})
	buf := &bytes.Buffer{}
	a.out = buf
	a.sidebarConfig.UI.NativeMenus = enabled
	return a, buf
}

// With native_menus off (the Mac screen), menus and dialogs write no signal.
func TestNativeMenuSignalOffWritesNothing(t *testing.T) {
	teststate.Lock(t)
	a, buf := nativeApp(t, false)
	nativeFrame(t, a, buf)
	a.openTabMenu(10, 0, 0, 0)
	if got := nativeFrame(t, a, buf); len(got) != 0 || bytes.Contains(buf.Bytes(), []byte("7767")) {
		t.Fatal("menu signal written while off", got)
	}
	nativeKey(a, uv.KeyEscape, "")
	a.renameWorkspaceDialog(0)
	if got := nativeFrame(t, a, buf); len(got) != 0 {
		t.Fatal("dialog signal written while off", got)
	}
}

// A real tab menu: one signal on open, none on an unchanged frame, a new
// sel after ↓, and "none" after Esc. A screen that connects with nothing
// open writes nothing.
func TestNativeMenuSignalMenuOpenMoveClose(t *testing.T) {
	teststate.Lock(t)
	a, buf := nativeApp(t, true)
	if got := nativeFrame(t, a, buf); len(got) != 0 {
		t.Fatal("nothing open must write nothing", got)
	}
	a.openTabMenu(10, 0, 0, 0)
	got := nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "menu" {
		t.Fatal("open", got)
	}
	items := got[0]["items"].([]any)
	if len(items) != len(a.menu.m.Items) || got[0]["title"] != a.menu.m.Title || got[0]["filterable"] != false {
		t.Fatal("payload", got[0])
	}
	for i, raw := range items {
		item := raw.(map[string]any)
		want := a.menu.m.Items[i]
		if item["label"] != want.Label || item["disabled"] != want.Disabled || item["separator"] != want.Separator {
			t.Fatal("item", i, item, want)
		}
		for _, k := range []string{"detail", "status"} {
			if _, ok := item[k]; !ok {
				t.Fatal("missing", k, item)
			}
		}
	}
	first := got[0]["sel"].(float64)
	if got := nativeFrame(t, a, buf); len(got) != 0 {
		t.Fatal("unchanged frame repeated the signal", got)
	}
	nativeKey(a, uv.KeyDown, "")
	got = nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "menu" || got[0]["sel"].(float64) == first || int(got[0]["sel"].(float64)) != a.menu.m.Sel {
		t.Fatal("down", first, got, a.menu.m.Sel)
	}
	nativeKey(a, uv.KeyEscape, "")
	if a.menu != nil {
		t.Fatal("esc must close")
	}
	got = nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "none" || len(got[0]) != 1 {
		t.Fatal("close", got)
	}
	if got := nativeFrame(t, a, buf); len(got) != 0 {
		t.Fatal("none repeated", got)
	}
}

// The rename dialog: open, typing changes input, Esc closes with "none".
func TestNativeMenuSignalDialog(t *testing.T) {
	teststate.Lock(t)
	a, buf := nativeApp(t, true)
	a.renameWorkspaceDialog(0)
	got := nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "dialog" || got[0]["title"] != "Rename workspace" || got[0]["confirm"] != "rename" || got[0]["cancel"] != "cancel" || got[0]["continue_only"] != false {
		t.Fatal("open", got)
	}
	start, ok := got[0]["input"].(string)
	if !ok || start != a.dialog.d.Input {
		t.Fatal("input", got[0]["input"])
	}
	if _, ok := got[0]["lines"].([]any); !ok {
		t.Fatal("lines must be an array", got[0])
	}
	if got := nativeFrame(t, a, buf); len(got) != 0 {
		t.Fatal("unchanged dialog repeated", got)
	}
	nativeKey(a, 'x', "x")
	got = nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["input"] != start+"x" {
		t.Fatal("typed", got)
	}
	nativeKey(a, uv.KeyEscape, "")
	got = nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "none" {
		t.Fatal("close", got)
	}
	// A dialog without a text field reports input as null.
	a.openQuitDialog()
	got = nativeFrame(t, a, buf)
	if len(got) != 1 || got[0]["kind"] != "dialog" || got[0]["input"] != nil {
		t.Fatal("quit dialog", got)
	}
	if v, present := got[0]["input"]; !present || v != nil {
		t.Fatal("input must be null", got[0])
	}
}
