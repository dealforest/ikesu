package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestPaneLinkActionsOnInitiatingDisplaySocket(t *testing.T) {
	teststate.Lock(t)
	s, _ := startClipboardEndpoint(t)
	openViewer := func(peer endpoint.Peer) *testConn {
		t.Helper()
		c := dialClient(t, s.dir)
		t.Cleanup(func() { c.conn.Close() })
		c.send(t, clientMsg{Type: "hello", Peer: peer, Cols: 120, Rows: 35})
		c.readUntil(t, 4*time.Second, func(m clientMsg) bool { return c.lastInput != nil })
		if c.lastInput == nil {
			t.Fatal("missing initial view")
		}
		return c
	}
	c := openViewer(endpoint.Current())
	other := openViewer(endpoint.Current())
	legacyPeer := endpoint.Current()
	legacyPeer.Methods = slices.DeleteFunc(legacyPeer.Methods, func(method string) bool { return method == endpoint.OpenLinkMessage })
	legacy := openViewer(legacyPeer)

	pid := c.lastInput.State.FocusedPane()
	url := "https://example.test/" + strings.Repeat("redirect_uri=a%2Fb&", 7) + "done"
	fixture := filepath.Join(t.TempDir(), "link.sh")
	if err := os.WriteFile(fixture, []byte("printf '\\033[2J\\033[H%s\\r\\nPANE_LINK_PROBE_READY' '"+url+"'\nexec /bin/cat\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.send(t, clientMsg{Type: "input", Data: encode64([]byte("/bin/sh '" + fixture + "'\n"))})
	waitRead(t, s.dir, fmt.Sprintf("p%d", pid), "PANE_LINK_PROBE_READY")
	region := c.lastInput.State.DisplayPaneRects()[0].Rect
	inner, _ := model.PaneContentRect(region, c.lastInput.State.PaneBorders)
	rightClickContinuation := func(view *testConn) []clientMsg {
		t.Helper()
		x, y := inner.X+2, inner.Y+2
		click := fmt.Sprintf("\x1b[<2;%d;%dM\x1b[<2;%d;%dm", x, y, x, y)
		view.send(t, clientMsg{Type: "input", Data: encode64([]byte(click))})
		messages := view.readUntil(t, 4*time.Second, func(m clientMsg) bool { return view.lastInput.Menu != nil })
		if view.lastInput.Menu == nil {
			t.Fatal("missing pane menu")
		}
		return messages
	}
	rightClickContinuation(c)
	if items := c.lastInput.Menu.Items; len(items) < 7 || items[3].Label != "Copy Link" || items[4].Label != "Open Link" || items[4].Disabled {
		t.Fatal("wrong link menu", items)
	}
	c.send(t, clientMsg{Type: "input", Data: encode64([]byte("\x1b[B\x1b[B\x1b[B\r"))})
	var opened string
	c.readUntil(t, 4*time.Second, func(m clientMsg) bool {
		if m.Type == endpoint.OpenLinkMessage {
			opened = m.Data
		}
		return opened != "" && c.lastInput.Menu == nil
	})
	if opened != url || c.lastInput.Menu != nil {
		t.Fatal("open action did not send the full URL and dismiss the menu", opened)
	}
	rightClickContinuation(c)
	c.send(t, clientMsg{Type: "input", Data: encode64([]byte("\x1b[B\x1b[B\r"))})
	wantCopy := "\x1b]52;c;" + encode64([]byte(url)) + "\a"
	got := c.readUntil(t, 4*time.Second, func(m clientMsg) bool {
		return m.Type == "output" && m.Data == encode64([]byte(wantCopy))
	})
	if !strings.Contains(outputs(got), wantCopy) {
		t.Fatal("copy action did not send the full URL")
	}
	rightClickContinuation(legacy)
	if !legacy.lastInput.Menu.Items[4].Disabled {
		t.Fatal("older viewer was offered an unsupported browser action")
	}

	// Flush queued messages on the second viewer with its own menu request.
	for _, m := range rightClickContinuation(other) {
		if m.Type == endpoint.OpenLinkMessage || m.Type == "output" && m.Data == encode64([]byte(wantCopy)) {
			t.Fatal("link action escaped the initiating viewer")
		}
	}
}
