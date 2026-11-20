package client

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/input"
	"testing"
)

func TestPhoneSpaceKeysConsumedWhenListClosed(t *testing.T) {
	for _, mobile := range []bool{true, false} {
		m, _ := nativeMenuClient(t, true)
		e := m.endpoints[m.active]
		s := e.snapshot.State
		if mobile {
			s.MobileWidthThreshold = 200
			e.snapshot.MobileSwitcher = false
		} else {
			s.MobileWidthThreshold = 0
			s.SidebarVisible = false
		}
		before := len(e.connection.queue)
		for _, key := range []rune{uv.KeyF9, uv.KeyF10} {
			for _, kind := range []input.Kind{input.KindKey, input.KindKeyRelease} {
				if err := m.input(input.Event{Kind: kind, Key: uv.Key{Code: key}, Raw: []byte("\x1b[20~")}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if len(e.connection.queue) != before {
			t.Fatal("reserved key leaked to endpoint", e.connection.queue)
		}
	}
}

func TestDesktopFunctionKeysStillReachEndpoint(t *testing.T) {
	m, _ := nativeMenuClient(t, false)
	e := m.endpoints[m.active]
	raw := []byte("\x1b[20~")
	if err := m.input(input.Event{Kind: input.KindKey, Key: uv.Key{Code: uv.KeyF9}, Raw: raw}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range e.connection.queue {
		if message.Type == "input" {
			found = true
		}
	}
	if !found {
		t.Fatal("desktop F9 did not reach endpoint")
	}
}
