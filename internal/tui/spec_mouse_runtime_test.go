package tui

import (
	"fmt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/teststate"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPECDoubleClickReleaseMenuOrFourApplicationEvents(t *testing.T) {
	teststate.Lock(t)
	for _, tracking := range []bool{false, true} {
		t.Run(fmt.Sprint(tracking), func(t *testing.T) {
			a := specRuntimeApp(t)
			pid := a.focusedPane()
			inner := a.innerRect(pid)
			path := filepath.Join(t.TempDir(), "received")
			expected := "\x1b[<0;3;3M\x1b[<0;3;3m\x1b[<0;3;3M\x1b[<0;3;3m"
			command := "printf READY; exec sleep 60"
			if tracking {
				command = fmt.Sprintf("stty raw -echo; printf '\\033[?1000h\\033[?1006hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", len(expected), path)
			}
			p, err := a.sup.SpawnWith(pid, a.cwd, inner.W, inner.H, nil, []string{"/bin/sh", "-c", command})
			if err != nil {
				t.Fatal(err)
			}
			specWaitText(t, p, "READY")
			m := uv.Mouse{X: inner.X + 2, Y: inner.Y + 2, Button: uv.MouseLeft}
			for i := 0; i < 2; i++ {
				a.onMouse(input.Event{Kind: input.KindMouseClick, Mouse: m})
				if a.menu != nil {
					t.Fatal("menu opened on press")
				}
				a.onMouse(input.Event{Kind: input.KindMouseRelease, Mouse: m})
				if i == 0 && a.menu != nil {
					t.Fatal("menu opened after first click")
				}
			}
			if tracking {
				if a.menu != nil || a.sel != nil {
					t.Fatal("application intercepted")
				}
				specWaitFile(t, path, []byte(expected))
			} else {
				if a.menu == nil {
					t.Fatal("second release missing menu")
				}
			}
		})
	}
}

func TestSPECWheelThreeRoutesActualPTYAndHostScroll(t *testing.T) {
	teststate.Lock(t)
	for _, mode := range []string{"reporting", "alternate", "host"} {
		t.Run(mode, func(t *testing.T) {
			a := specRuntimeApp(t)
			pid := a.focusedPane()
			inner := a.innerRect(pid)
			path := filepath.Join(t.TempDir(), "wheel")
			expected := ""
			command := "i=0; while [ $i -lt 100 ]; do printf 'line %s\\r\\n' $i; i=$((i+1)); done; printf READY; exec sleep 60"
			if mode == "reporting" {
				expected = "\x1b[<64;3;3M"
				command = fmt.Sprintf("stty raw -echo; printf '\\033[?1000h\\033[?1006hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", len(expected), path)
			}
			if mode == "alternate" {
				expected = "\x1b[A"
				command = fmt.Sprintf("stty raw -echo; printf '\\033[?1049hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec sleep 60", len(expected), path)
			}
			p, err := a.sup.SpawnWith(pid, a.cwd, inner.W, inner.H, nil, []string{"/bin/sh", "-c", command})
			if err != nil {
				t.Fatal(err)
			}
			specWaitText(t, p, "READY")
			a.onMouse(input.Event{Kind: input.KindMouseWheel, Mouse: uv.Mouse{X: inner.X + 2, Y: inner.Y + 2, Button: uv.MouseWheelUp}})
			if mode == "host" {
				if a.scroll[pid] != 3 {
					t.Fatal("host notch", a.scroll[pid])
				}
			} else {
				specWaitFile(t, path, []byte(expected))
				if a.scroll[pid] != 0 {
					t.Fatal("application wheel scrolled host")
				}
			}
		})
	}
}

func TestZoomedPaneReceivesMouseAtHiddenSplit(t *testing.T) {
	teststate.Lock(t)
	a := specRuntimeApp(t)
	pid := a.focusedPane()
	tab := a.st.ActiveWorkspace().TabRef()
	tab.Layout.SplitFocused(layout.Vertical, pid+1)
	border := tab.Layout.Splits(a.st.PanesArea())[0]
	if err := a.exec(model.CmdZoomPane{Pane: pid, Mode: "on"}); err != nil {
		t.Fatal(err)
	}
	inner := a.innerRect(pid)
	y := inner.Y + 2
	path := filepath.Join(t.TempDir(), "mouse")
	var want strings.Builder
	for _, x := range []int{border.Pos - 1, border.Pos, border.Pos + 1} {
		fmt.Fprintf(&want, "\x1b[<0;%d;3M\x1b[<32;%d;3M\x1b[<0;%d;3m", x-inner.X+1, x-inner.X+5, x-inner.X+5)
	}
	command := fmt.Sprintf("stty raw -echo; printf '\\033[?1000h\\033[?1006hREADY'; dd bs=1 count=%d of='%s' 2>/dev/null; exec /bin/cat", want.Len(), path)
	p, err := a.sup.SpawnWith(pid, a.cwd, inner.W, inner.H, nil, []string{"/bin/sh", "-c", command})
	if err != nil {
		t.Fatal(err)
	}
	specWaitText(t, p, "READY")
	a.draw()
	for _, x := range []int{border.Pos - 1, border.Pos, border.Pos + 1} {
		specFeed(a, fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<32;%d;%dM\x1b[<0;%d;%dm", x+1, y+1, x+5, y+1, x+5, y+1))
		if ratio := tab.Layout.Root().Ratio; ratio != border.Ratio {
			t.Fatalf("press at column %d changed the hidden split ratio from %g to %g", x, border.Ratio, ratio)
		}
	}
	specWaitFile(t, path, []byte(want.String()))
}
