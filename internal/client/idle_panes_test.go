package client

import (
	"errors"
	"fmt"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/runtime"
	"strings"
	"testing"
	"time"
)

func TestIdleInitiallySelected(t *testing.T) {
	for _, tc := range []struct {
		name, state                        string
		pending, focused, hq, active, want bool
	}{
		{"idle", "idle", false, false, false, true, true}, {"done", "done", false, false, false, true, true},
		{"working", "working", false, false, false, true, false}, {"blocked", "blocked", false, false, false, true, false}, {"unknown", "unknown", false, false, false, true, false},
		{"pending", "idle", true, false, false, true, false}, {"focused", "idle", false, true, false, true, false}, {"remote focus", "idle", false, true, false, false, true}, {"HQ", "idle", false, false, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := endpoint.IdlePane{State: tc.state, PendingInput: tc.pending, InputKnown: true, Focused: tc.focused}
			if got := idleInitiallySelected(p, tc.hq, tc.active); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIdlePanesOldAndNewServers(t *testing.T) {
	h := newRightMachineHarness(t, true, true)
	m := h.m
	m.openIdlePanes()
	h.until("new server list", func() bool { return m.idle != nil && !m.idle.loading })
	if len(m.idle.entries) != 2 || !strings.Contains(strings.Join(m.idle.dialog(200).Lines, "\n"), "remote: unsupported") {
		t.Fatalf("mixed server list: %+v %+v", m.idle.entries, m.idle.dialog(200).Lines)
	}
	m.idle.entries[0].selected = true
	m.deleteIdlePanes()
	h.until("mixed server delete", func() bool { return m.idle == nil })
	if m.idleToast != "ペインを 1 個閉じた · 失敗 0 · 一覧を取れなかった: remote: unsupported" {
		t.Fatal(m.idleToast)
	}
}

func TestIdlePanesDisconnectAfterList(t *testing.T) {
	h := newRightMachineHarness(t, true)
	m := h.m
	m.openIdlePanes()
	h.until("both lists", func() bool { return m.idle != nil && !m.idle.loading })
	for i := range m.idle.entries {
		m.idle.entries[i].selected = true
	}
	m.idle.cursor = 3
	m.idle.confirming = true
	h.disconnectIdle("remote")
	if len(m.idle.entries) != 2 || m.idle.selected() != 2 || m.idle.cursor != 1 || !strings.Contains(m.idle.dialog(200).Detail, "2 個のペインを閉じて") {
		t.Fatalf("disconnect: %+v dialog=%+v", m.idle, m.idle.dialog(200))
	}
	m.deleteIdlePanes()
	h.until("remaining server delete", func() bool { return m.idle == nil })
	if m.idleToast != "ペインを 1 個閉じた · 失敗 1" {
		t.Fatal(m.idleToast)
	}
}

func (h *rightMachineHarness) disconnectIdle(id string) {
	h.t.Helper()
	if err := h.m.endpoints[id].connection.stream.Close(); err != nil {
		h.t.Fatal(err)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-h.m.events:
			if event.id == id && event.err == nil {
				continue
			}
			if _, done := h.m.event(event); done {
				h.t.Fatal("client exited after disconnect")
			}
			if event.id == id && event.err != nil {
				return
			}
		case <-timer.C:
			h.t.Fatal("disconnect event not received")
		}
	}
}

func TestIdlePanesDisconnectWhileLoading(t *testing.T) {
	h := newRightMachineHarness(t, true)
	m := h.m
	m.openIdlePanes()
	if m.idle.pending["remote"] != 1 {
		t.Fatal("remote list not pending")
	}
	h.disconnectIdle("remote")
	h.until("remaining list", func() bool { return m.idle != nil && !m.idle.loading })
	if m.idle.pending["remote"] != 0 || !strings.Contains(strings.Join(m.idle.dialog(200).Lines, "\n"), "remote:") {
		t.Fatalf("disconnect while loading: %+v", m.idle)
	}
}

func TestIdlePanesDisconnectWhileDeleting(t *testing.T) {
	h := newRightMachineHarness(t, true)
	m := h.m
	m.openIdlePanes()
	h.until("both lists", func() bool { return m.idle != nil && !m.idle.loading })
	for i := range m.idle.entries {
		m.idle.entries[i].selected = m.idle.entries[i].machine == "remote" && m.idle.entries[i].pane.ID == 1
	}
	m.deleteIdlePanes()
	if m.idle == nil || m.idle.pending["remote"] != 1 {
		t.Fatal("delete not pending")
	}
	h.disconnectIdle("remote")
	if m.idle != nil || m.idleToast != "ペインを 0 個閉じた · 失敗 1" {
		t.Fatalf("disconnect while deleting: %+v toast=%q", m.idle, m.idleToast)
	}
	if err := m.input(input.Event{Kind: input.KindKey, Key: uv.Key{Code: uv.KeyEscape}, Raw: []byte{27}}); err != nil {
		t.Fatal(err)
	}
	if err := m.input(input.Event{Kind: input.KindKey, Key: uv.Key{Code: 'Z'}, Raw: []byte("ZZ_IDLE_INPUT_OK")}); err != nil {
		t.Fatal(err)
	}
	sent := m.endpoints["local"].connection.sentInput
	if sent < 2 {
		t.Fatalf("Esc and input were not sent: %d", sent)
	}
	h.until("local input after disconnect", func() bool {
		return m.endpoints["local"].processedInput >= sent
	})
}

func TestIdlePanesStaleDisconnectPreservesConnection(t *testing.T) {
	h := newRightMachineHarness(t, true)
	m := h.m
	m.openIdlePanes()
	h.until("both lists", func() bool { return m.idle != nil && !m.idle.loading })
	e := m.endpoints["remote"]
	e.attempt++
	conn, count := e.connection, len(m.idle.entries)
	m.event(endpointEvent{id: "remote", attempt: e.attempt - 1, err: errors.New("old connection")})
	if e.connection != conn || len(m.idle.entries) != count {
		t.Fatal("stale error changed current connection or list")
	}
}

func TestIdleAgeDisplay(t *testing.T) {
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{59 * time.Second, "0m"}, {59 * time.Minute, "59m"},
		{time.Hour, "1h 0m"}, {2*time.Hour + 4*time.Minute, "2h 4m"},
		{24*time.Hour - time.Minute, "23h 59m"}, {24 * time.Hour, "1d 0h"},
		{52 * time.Hour, "2d 4h"},
	} {
		if got := idleAge(tc.age); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.age, got, tc.want)
		}
	}
}

func TestIdleUnknownInputIsNotInitiallySelected(t *testing.T) {
	if idleInitiallySelected(endpoint.IdlePane{State: "idle"}, false, true) {
		t.Fatal("unrecognized prompt must not be assumed empty")
	}
}

func TestIdleUnknownInputCanBeSelectedManually(t *testing.T) {
	m := &machineClient{idle: &idleDialog{entries: []idleEntry{{pane: endpoint.IdlePane{State: "idle", AgeNanos: int64(48 * time.Hour)}}}}}
	m.idleInput(input.Event{Kind: input.KindKey, Key: uv.Key{Code: ' '}})
	if m.idle.selected() != 1 {
		t.Fatal("manual selection of an unknown input failed")
	}
}
func TestIdleThresholdAndSelection(t *testing.T) {
	d := idleDialog{entries: []idleEntry{{pane: endpoint.IdlePane{AgeNanos: int64(8 * 24 * time.Hour)}, selected: true}, {pane: endpoint.IdlePane{AgeNanos: int64(2 * 24 * time.Hour)}, selected: true}, {pane: endpoint.IdlePane{AgeNanos: int64(24 * time.Hour)}, selected: true}}}
	if got := len(d.visible()); got != 2 {
		t.Fatalf("24h: %d", got)
	}
	d.threshold = 1
	if got := len(d.visible()); got != 1 {
		t.Fatalf("3d: %d", got)
	}
	d.threshold = 2
	if got := d.selected(); got != 1 {
		t.Fatalf("1w: %d", got)
	}
}

// Two isolated server processes and their PTYs exercise the cross-machine deletion path.
func TestIdlePanesTwoServersActualPTY(t *testing.T) {
	h := newRightMachineHarness(t, true)
	m := h.m
	for _, id := range m.order {
		for pane := 1; pane <= 2; pane++ {
			h.api(id, "pane.report_agent", map[string]any{"pane_id": fmt.Sprintf("p%d", pane), "agent": "codex", "source": "test", "state": "idle"})
		}
	}
	m.openIdlePanes()
	h.until("idle lists from both servers", func() bool { return m.idle != nil && !m.idle.loading })
	if len(m.idle.entries) != 4 {
		t.Fatalf("expected both servers: %+v", m.idle.entries)
	}
	for _, e := range m.idle.entries {
		// Each server measures the pane's shell tree with one process sample.
		if e.pane.MemoryKiB <= 0 {
			t.Fatalf("pane memory not measured: %+v", e.pane)
		}
	}
	for i := range m.idle.entries {
		m.idle.entries[i].selected = m.idle.entries[i].pane.ID == 1
	}
	if m.idle.selected() != 2 {
		t.Fatalf("expected one pane per machine selected: %+v", m.idle.entries)
	}
	m.idle.confirming = true
	m.deleteIdlePanes()
	h.until("idle delete results", func() bool { return m.idle == nil })
	if m.idleToast != "ペインを 2 個閉じた · 失敗 0" {
		t.Fatal(m.idleToast)
	}
	for _, id := range m.order {
		h.until("remaining PTY", func() bool { return len(m.endpoints[id].snapshot.State.Workspaces[0].Tabs[0].Layout.PaneIDs()) == 1 })
	}
}

func TestIdleDraftSelectionFromRealPane(t *testing.T) {
	for _, tc := range []struct {
		name, agent, screen string
		want, known         bool
	}{
		{"empty Codex", "codex", "› \x1b[2mAsk Codex to do anything\x1b[0m", true, true},
		{"Codex draft", "codex", "› unfinished", false, true},
		{"Claude draft", "claude", "❯ unfinished", false, true},
		{"unknown agent", "muse", "⟩ unfinished", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sup := runtime.NewSupervisor()
			defer sup.KillAll()
			p, err := sup.SpawnWith(1, t.TempDir(), 80, 24, nil, []string{"/bin/cat"})
			if err != nil {
				t.Fatal(err)
			}
			p.SetAgent(tc.agent)
			p.WithEmu(func(e *vt.Emulator) { _, _ = e.Write([]byte("\x1b[H\x1b[2J" + tc.screen)) })
			pending, known := p.UnsentAgentInput()
			if known != tc.known || idleInitiallySelected(endpoint.IdlePane{State: "idle", PendingInput: pending, InputKnown: known}, false, true) != tc.want {
				t.Fatalf("pending=%v known=%v", pending, known)
			}
		})
	}
}

func TestIdleDialogShowsLastPaneKept(t *testing.T) {
	d := idleDialog{kept: 1}
	if !strings.Contains(strings.Join(d.dialog(200).Lines, "\n"), "最後の1個のペインは閉じずに残しています") {
		t.Fatal("last pane notice missing")
	}
}

func TestIdleKeepReasons(t *testing.T) {
	for _, tc := range []struct {
		name       string
		p          endpoint.IdlePane
		hq, active bool
		want       string
	}{
		{"closes", endpoint.IdlePane{State: "idle", InputKnown: true}, false, true, ""},
		{"done closes", endpoint.IdlePane{State: "done", InputKnown: true}, false, true, ""},
		{"HQ", endpoint.IdlePane{State: "idle", InputKnown: true}, true, true, "HQ"},
		{"looking", endpoint.IdlePane{State: "idle", InputKnown: true, Focused: true}, false, true, "今見ている"},
		{"remote focus closes", endpoint.IdlePane{State: "idle", InputKnown: true, Focused: true}, false, false, ""},
		{"working", endpoint.IdlePane{State: "working", InputKnown: true}, false, true, "動作中・確認待ち（working）"},
		{"unknown input", endpoint.IdlePane{State: "idle"}, false, true, "入力欄を読めない"},
		{"draft", endpoint.IdlePane{State: "idle", InputKnown: true, PendingInput: true}, false, true, "入力途中の文字がある"},
	} {
		if got := idleKeepReason(tc.p, tc.hq, tc.active); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// The list says what closing does, what [x] and [ ] mean, why a pane is kept,
// and how much memory the checked panes hold.
func TestIdleDialogExplainsChecksAndMemory(t *testing.T) {
	day := int64(48 * time.Hour)
	d := idleDialog{entries: []idleEntry{
		{label: "Local", pane: endpoint.IdlePane{ID: 3, AgeNanos: day, MemoryKiB: 300 * 1024}, selected: true},
		{label: "mini", pane: endpoint.IdlePane{ID: 4, AgeNanos: day, MemoryKiB: 212 * 1024}, keep: "入力途中の文字がある"},
	}}
	box := d.dialog(200)
	text := box.Detail + "\n" + strings.Join(box.Lines, "\n")
	for _, want := range []string{"マージとは関係ありません", "worktree・ブランチ・ファイルは消しません", "該当 2 個 · [x] 閉じる 1 個 · [ ] 残す 1 個", "メモリ（RSS 合計の目安）: 約 300 MiB", "> [x] p3 · Local", "300 MiB", "[ ] p4 [残す:入力途中] · mini", "p3: 閉じる（待機中か完了で、入力途中の文字がない）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if box.Confirm != "review" || box.ContinueOnly {
		t.Fatalf("selection must lead to the review: %+v", box)
	}
	d.confirming = true
	confirm := d.dialog(200)
	if !strings.Contains(confirm.Detail, "1 個のペインを閉じて") || !strings.Contains(strings.Join(confirm.Lines, "\n"), "メモリ（RSS 合計の目安）: 約 300 MiB") {
		t.Fatalf("review: %+v", confirm)
	}
}

func TestIdleDialogEmptyListSaysSoAndEnterCloses(t *testing.T) {
	m := &machineClient{idle: &idleDialog{entries: []idleEntry{{pane: endpoint.IdlePane{AgeNanos: int64(time.Hour)}}}}}
	box := m.idle.dialog(200)
	if text := strings.Join(box.Lines, "\n"); !strings.Contains(text, "24h以上止まっているペインはありません（調べたエージェントのペイン 1 個）") {
		t.Fatal(text)
	}
	if !box.ContinueOnly || box.Confirm != "close" {
		t.Fatalf("empty list must offer only close: %+v", box)
	}
	m.idleInput(input.Event{Kind: input.KindKey, Key: uv.Key{Code: uv.KeyEnter}})
	if m.idle != nil {
		t.Fatal("enter on an empty list must close the dialog")
	}
}

func TestIdleDialogShowsEachMachineWhileLoading(t *testing.T) {
	machines := []idleMachine{{"local", "Local"}, {"mini", "Mac mini"}, {"old", "Old"}}
	d := idleDialog{loading: true, machines: machines, pending: map[string]int{"mini": 1}, status: map[string]string{"local": "✓", "mini": "…", "old": "✗"}}
	if got := d.dialog(200); !strings.Contains(got.Detail, "Local ✓ · Mac mini … · Old ✗") || got.Confirm != "close" || !got.ContinueOnly {
		t.Fatalf("%+v", got)
	}
	// Closing shows only the machines asked to close panes, and no buttons.
	d.loading, d.deleting, d.targets = false, true, machines[1:2]
	if got := d.dialog(200); got.Detail != "ペインを閉じています… Mac mini …" || !got.NoButtons {
		t.Fatalf("%+v", got)
	}
}

func TestIdleDialogFitsTenLines(t *testing.T) {
	d := idleDialog{kept: 1, failures: []idleFailure{{"mini", "unsupported"}}}
	for i := 0; i < 20; i++ {
		d.entries = append(d.entries, idleEntry{pane: endpoint.IdlePane{ID: i, AgeNanos: int64(48 * time.Hour)}, selected: true})
	}
	d.cursor = 19
	lines := d.dialog(200).Lines
	if len(lines) > idleDialogLines {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"> [x] p19", "最後の1個", "mini: unsupported", "space で切替"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, lines)
		}
	}
}

// A pane checked by hand after it started unchecked reads as a warning, not
// as "kept", and the line under the list says it will close.
func TestIdleManualSelectionShowsWarningNotKeep(t *testing.T) {
	day := int64(48 * time.Hour)
	m := &machineClient{idle: &idleDialog{entries: []idleEntry{
		{label: "mini", pane: endpoint.IdlePane{ID: 4, AgeNanos: day, State: "idle", InputKnown: true, PendingInput: true}, keep: "入力途中の文字がある"},
		{label: "mini", pane: endpoint.IdlePane{ID: 5, AgeNanos: day, State: "working", InputKnown: true}, keep: "動作中・確認待ち（working）"},
	}}}
	m.idleInput(input.Event{Kind: input.KindKey, Key: uv.Key{Code: ' '}})
	text := strings.Join(m.idle.dialog(200).Lines, "\n")
	for _, want := range []string{"> [x] p4 [注意:入力途中]", "[ ] p5 [残す:動作中]", "p4: 閉じる。ただし 入力途中の文字がある。"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "[x] p4 [残す") {
		t.Fatalf("checked row still says keep:\n%s", text)
	}
	m.idleInput(input.Event{Kind: input.KindKey, Key: uv.Key{Code: 'a'}})
	text = strings.Join(m.idle.dialog(200).Lines, "\n")
	if !strings.Contains(text, "[x] p5 [注意:動作中]") || m.idle.selected() != 2 {
		t.Fatalf("select all:\n%s", text)
	}
}

// A machine that could not list its panes does not open the close review
// when nothing is checked; enter closes the dialog instead.
func TestIdleFailureAloneDoesNotOpenReview(t *testing.T) {
	m := &machineClient{idle: &idleDialog{failures: []idleFailure{{"old", "unsupported"}}}}
	box := m.idle.dialog(200)
	if !box.ContinueOnly || box.Confirm != "close" || !strings.Contains(strings.Join(box.Lines, "\n"), "一覧を取れなかった: old: unsupported") {
		t.Fatalf("%+v", box)
	}
	m.idleInput(input.Event{Kind: input.KindKey, Key: uv.Key{Code: uv.KeyEnter}})
	if m.idle != nil {
		t.Fatal("enter must close, not review zero panes")
	}
}

// At 80 columns the note about merges stays whole and every row keeps its
// check box, tag, age and memory; the long names give way.
func TestIdleDialogAtEightyColumns(t *testing.T) {
	long := strings.Repeat("very-long-workspace-name-", 6)
	d := idleDialog{entries: []idleEntry{
		{label: "Mac mini", pane: endpoint.IdlePane{ID: 7, AgeNanos: int64(50 * time.Hour), Agent: "codex", WorkspaceName: long, TabName: "main", MemoryKiB: 512 * 1024}, keep: "入力途中の文字がある"},
	}}
	box := d.dialog(80)
	text := strings.Join(box.Lines, "\n")
	for _, want := range []string{"マージとは関係ありません。", "worktree・ブランチ・ファイルは消しません。", "[ ] p7 [残す:入力途中]", "2d 2h止まっている · 512 MiB", "p7: 残す（入力途中の文字がある）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	for _, line := range append(box.Lines, box.Detail) {
		if w := ansi.StringWidth(line); w > 72 {
			t.Fatalf("line wider than 72 cells (%d): %q", w, line)
		}
	}
}

func TestIdleMemoryTextNamesUnmeasuredPanes(t *testing.T) {
	day := int64(48 * time.Hour)
	d := idleDialog{entries: []idleEntry{
		{pane: endpoint.IdlePane{ID: 1, AgeNanos: day, MemoryKiB: 100 * 1024}, selected: true},
		{pane: endpoint.IdlePane{ID: 2, AgeNanos: day}, selected: true},
	}}
	if got := d.memoryText(); got != "メモリ（RSS 合計の目安）: 約 100 MiB · 測れなかった 1 個" {
		t.Fatal(got)
	}
	d.entries[0].selected = false
	if got := d.memoryText(); got != "メモリ（RSS）: 測れなかった" {
		t.Fatal(got)
	}
}

// Many failed machines fold into one line, so the list still fits 10 lines
// with its key help; the empty message counts panes a server kept.
func TestIdleManyFailuresStillFit(t *testing.T) {
	d := idleDialog{entries: []idleEntry{{pane: endpoint.IdlePane{ID: 1, AgeNanos: int64(48 * time.Hour)}, selected: true}}}
	for i := 0; i < 6; i++ {
		d.failures = append(d.failures, idleFailure{fmt.Sprintf("m%d", i), "unsupported"})
	}
	lines := d.dialog(200).Lines
	if len(lines) > idleDialogLines || !strings.Contains(strings.Join(lines, "\n"), "space で切替") {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	empty := idleDialog{kept: 1}
	if text := strings.Join(empty.dialog(200).Lines, "\n"); !strings.Contains(text, "調べたエージェントのペイン 1 個") {
		t.Fatal(text)
	}
}
