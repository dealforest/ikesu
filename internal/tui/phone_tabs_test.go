package tui

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
	"github.com/kazuph/ikesu/internal/teststate"
	"io"
	"testing"
)

func TestPhoneSwitcherBothTabRowsSelect(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	var cfg config.Config
	cfg.UI.NativeMenus = true
	a.SetSidebarConfig(cfg, true)
	a.exec(model.CmdResize{Cols: 54, Rows: 20})
	a.exec(model.CmdNewTab{Workspace: 0})
	a.exec(model.CmdSelectTab{Workspace: 0, Tab: 0})
	rows := render.MobileRows(a.st, a.mainMenuItems())
	index := -1
	for i, r := range rows {
		if r.Kind == render.RowMobileTab && r.Line == 1 && !r.Continuation {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("tab absent")
	}
	for _, delta := range []int{0, 1} {
		a.exec(model.CmdSelectTab{Workspace: 0, Tab: 0})
		a.mobileSwitcher = true
		a.mobileScroll = index - 1
		sidebarEvent(a, input.KindMouseClick, 4, render.MobileViewport(a.st).Y+1+delta)
		if a.st.ActiveWorkspace().ActiveTab != 1 || a.mobileSwitcher {
			t.Fatalf("row %d did not select", delta)
		}
	}
}

func TestPhoneHorizontalBothTabRowsSelect(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	var cfg config.Config
	cfg.UI.NativeMenus = true
	a.SetSidebarConfig(cfg, true)
	a.exec(model.CmdResize{Cols: 100, Rows: 35})
	a.exec(model.CmdNewTab{Workspace: 0})
	a.exec(model.CmdSelectTab{Workspace: 0, Tab: 0})
	for _, y := range []int{0, 1} {
		a.exec(model.CmdSelectTab{Workspace: 0, Tab: 0})
		out := render.Compose(render.Input{State: a.st})
		a.tabHits = out.TabHits
		r := out.TabHits[1]
		if r.H != 2 {
			t.Fatal(r)
		}
		sidebarEvent(a, input.KindMouseClick, r.X+1, y)
		sidebarEvent(a, input.KindMouseRelease, r.X+1, y)
		if a.st.ActiveWorkspace().ActiveTab != 1 {
			t.Fatalf("row %d did not select", y)
		}
	}
}

func TestPhoneTabsViewersStayIndependent(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	phone := a.NewViewer(io.Discard, 100, 35, nil)
	desktop := a.NewViewer(io.Discard, 100, 35, nil)
	var native, normal config.Config
	native.UI.NativeMenus = true
	phone.Configure(a, &native, nil, nil)
	desktop.Configure(a, &normal, nil, nil)
	phone.with(a, func(ui *App) {
		if !ui.st.PhoneTabRows || ui.st.TabBarArea().H != 2 || ui.st.PanesArea().Y != 2 {
			t.Fatal("phone layout", ui.st.TabBarArea(), ui.st.PanesArea())
		}
	})
	desktop.with(a, func(ui *App) {
		if ui.st.PhoneTabRows || ui.st.TabBarArea().H != 1 || ui.st.PanesArea().Y != 1 {
			t.Fatal("desktop layout", ui.st.TabBarArea(), ui.st.PanesArea())
		}
	})
	if a.st.PhoneTabRows {
		t.Fatal("leaked viewer setting to session")
	}
}

func TestPhoneSpaceKeysCycleAndKeepList(t *testing.T) {
	teststate.Lock(t)
	for _, width := range []int{54, 100} {
		a := specRuntimeApp(t)
		var cfg config.Config
		cfg.UI.NativeMenus = true
		a.SetSidebarConfig(cfg, true)
		a.exec(model.CmdResize{Cols: width, Rows: 35})
		a.exec(model.CmdNewWorkspace{Cwd: t.TempDir()})
		a.exec(model.CmdNewWorkspace{Cwd: t.TempDir()})
		a.exec(model.CmdSelectWorkspace{Index: 0})
		if a.st.Mobile() {
			a.mobileSwitcher = true
		} else {
			a.st.SidebarVisible = true
		}
		order := a.visibleWorkspaceOrder()
		if len(order) != 3 {
			t.Fatal(order)
		}
		a.handleKey(uv.Key{Code: uv.KeyF9}, []byte("\x1b[20~"))
		if a.st.Active != order[len(order)-1] {
			t.Fatal("previous wrap", width, a.st.Active, order)
		}
		a.handleKey(uv.Key{Code: uv.KeyF10}, []byte("\x1b[21~"))
		if a.st.Active != order[0] {
			t.Fatal("next wrap", width, a.st.Active, order)
		}
		if a.st.Mobile() && !a.mobileSwitcher || !a.st.Mobile() && !a.st.SidebarVisible {
			t.Fatal("list closed", width)
		}
		if a.st.Mobile() {
			a.mobileSwitcher = false
		} else {
			a.st.SidebarVisible = false
		}
		a.handleKey(uv.Key{Code: uv.KeyF10}, []byte("\x1b[21~"))
		if a.st.Active != order[0] {
			t.Fatal("closed list changed workspace", width)
		}
	}
}

func TestPhoneSpaceKeysThroughViewerInput(t *testing.T) {
	teststate.Lock(t)
	for _, cols := range []int{54, 100} {
		a := specRuntimeApp(t)
		a.exec(model.CmdNewWorkspace{Cwd: t.TempDir()})
		a.exec(model.CmdSelectWorkspace{Index: 0})
		v := a.NewViewer(io.Discard, cols, 35, nil)
		var cfg config.Config
		cfg.UI.NativeMenus = true
		v.Configure(a, &cfg, nil, nil)
		v.with(a, func(ui *App) {
			if ui.st.Mobile() {
				ui.mobileSwitcher = true
			} else {
				ui.st.SidebarVisible = true
			}
		})
		v.Input(a, []byte("\x1b[21~"))
		v.with(a, func(ui *App) {
			if ui.st.Active != 1 {
				t.Fatalf("width %d F10 did not advance", cols)
			}
			if ui.st.Mobile() && !ui.mobileSwitcher || !ui.st.Mobile() && !ui.st.SidebarVisible {
				t.Fatal("list closed")
			}
		})
		v.Input(a, []byte("\x1b[20~"))
		v.with(a, func(ui *App) {
			if ui.st.Active != 0 {
				t.Fatalf("width %d F9 did not go back", cols)
			}
		})
	}
}
