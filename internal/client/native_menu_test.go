package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/render"
)

var clientNativeOSC = regexp.MustCompile("\x1b\\]7767;ikesu-ui;([A-Za-z0-9+/=]*)\x07")

// paintSignals paints once and decodes the ikesu-ui signals in what this
// paint appended to the screen output.
func paintSignals(t *testing.T, m *machineClient, file *os.File) []map[string]any {
	t.Helper()
	before, err := file.Seek(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.paint(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	for _, match := range clientNativeOSC.FindAllSubmatch(data[before:], -1) {
		raw, err := base64.StdEncoding.DecodeString(string(match[1]))
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if !bytes.Contains(data[before:], []byte("7767")) && len(got) != 0 {
		t.Fatal("decoder mismatch")
	}
	return got
}

func nativeMenuClient(t *testing.T, enabled bool) (*machineClient, *os.File) {
	t.Helper()
	m := searchFixture(t)
	text := "[ui]\nnative_menus=false\n"
	if enabled {
		text = "[ui]\nnative_menus=true\n"
	}
	cfg, err := config.Decode([]byte(text))
	if err != nil || len(cfg.Warnings) != 0 {
		t.Fatal(cfg.Warnings, err)
	}
	if cfg.UI.NativeMenus != enabled {
		t.Fatal("native_menus not decoded")
	}
	m.settings = &localSettings{config: &cfg}
	file, err := os.CreateTemp(t.TempDir(), "term")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	m.out = file
	return m, file
}

// The attached client composes the server's snapshot; the signal follows
// the snapshot's menu and dialog, once per change, only when enabled.
func TestClientNativeMenuSignal(t *testing.T) {
	m, file := nativeMenuClient(t, true)
	e := m.endpoints[m.active]
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal("nothing open at connect must write nothing", got)
	}
	e.snapshot.Menu = &render.Menu{Title: "tab", Sel: 0, Hover: -1, Items: []render.MenuItem{{Label: "Rename"}, {Separator: true}, {Label: "Close", Detail: "x", Status: "busy", Disabled: true}}}
	got := paintSignals(t, m, file)
	if len(got) != 1 || got[0]["kind"] != "menu" || got[0]["sel"] != float64(0) || len(got[0]["items"].([]any)) != 3 {
		t.Fatal("open", got)
	}
	closeItem := got[0]["items"].([]any)[2].(map[string]any)
	if closeItem["detail"] != "x" || closeItem["status"] != "busy" || closeItem["disabled"] != true {
		t.Fatal("item", closeItem)
	}
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal("same frame repeated", got)
	}
	e.snapshot.Menu.Hover = 2 // pointer movement alone is not a change
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal("hover repeated", got)
	}
	e.snapshot.Menu.Sel = 2
	if got := paintSignals(t, m, file); len(got) != 1 || got[0]["sel"] != float64(2) {
		t.Fatal("sel", got)
	}
	e.snapshot.Menu = nil
	if got := paintSignals(t, m, file); len(got) != 1 || got[0]["kind"] != "none" {
		t.Fatal("close", got)
	}
	e.snapshot.Dialog = &render.Dialog{Title: "Stop server?", Detail: "d", Confirm: "stop", Cancel: "cancel", ContinueOnly: true}
	got = paintSignals(t, m, file)
	if len(got) != 1 || got[0]["kind"] != "dialog" || got[0]["input"] != nil || got[0]["continue_only"] != true {
		t.Fatal("dialog", got)
	}
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal("dialog repeated", got)
	}
	e.snapshot.Dialog = nil
	if got := paintSignals(t, m, file); len(got) != 1 || got[0]["kind"] != "none" {
		t.Fatal("dialog close", got)
	}
}

func TestClientNativeMenuSignalOff(t *testing.T) {
	m, file := nativeMenuClient(t, false)
	e := m.endpoints[m.active]
	e.snapshot.Menu = &render.Menu{Title: "tab", Items: []render.MenuItem{{Label: "Rename"}}}
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal(got)
	}
	e.snapshot.Menu, e.snapshot.Dialog = nil, &render.Dialog{Title: "x"}
	if got := paintSignals(t, m, file); len(got) != 0 {
		t.Fatal(got)
	}
	data, _ := os.ReadFile(file.Name())
	if bytes.Contains(data, []byte("\x1b]7767")) {
		t.Fatal("signal written while off")
	}
}
