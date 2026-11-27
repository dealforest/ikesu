package client

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/layout"
	"github.com/kazuph/ikesu/internal/render"
)

type idleEntry struct {
	machine, label, server string
	pane                   endpoint.IdlePane
	selected               bool
	keep                   string // why the pane starts unchecked; "" when it starts checked
}
type idleFailure struct{ label, reason string }
type idleDialog struct {
	request                       uint64
	entries                       []idleEntry
	failures                      []idleFailure
	pending                       map[string]int
	machines                      []idleMachine     // machines asked for a list, in sidebar order
	targets                       []idleMachine     // machines asked to close panes
	status                        map[string]string // machine → "…" waiting, "✓" answered, "✗" failed
	threshold, cursor             int
	loading, confirming, deleting bool
	deleted, failed               int
	kept                          int // panes a server kept because each was its last one
}

type idleMachine struct{ id, label string }

// idleDialogLines is how many body lines render.Dialog draws.
const idleDialogLines = 10

var idleThresholds = []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour}
var idleThresholdLabels = []string{"24h", "3d", "1w"}

func (m *machineClient) openIdlePanes() {
	m.idleRequest++
	d := &idleDialog{request: m.idleRequest, pending: map[string]int{}, status: map[string]string{}, loading: true}
	m.idle = d
	for _, id := range m.order {
		e := m.endpoints[id]
		if e == nil || e.connection == nil || e.snapshotAttempt != e.attempt {
			continue
		}
		d.machines = append(d.machines, idleMachine{id, e.profile.Label})
		if !endpoint.Allows(e.connection.writer.available, endpoint.RequiredGeneration("idle_panes"), e.connection.writer.methods, "idle_panes") {
			d.failures = append(d.failures, idleFailure{e.profile.Label, "unsupported"})
			d.status[id] = "✗"
			continue
		}
		if err := e.connection.enqueue(msg{Type: "idle_panes", IdleRequest: d.request}); err == nil {
			d.pending[id] = 1
			d.status[id] = "…"
		} else {
			d.failures = append(d.failures, idleFailure{e.profile.Label, err.Error()})
			d.status[id] = "✗"
		}
	}
	if len(d.pending) == 0 {
		d.loading = false
	}
}

func (m *machineClient) idleListResult(id string, request uint64, list *endpoint.IdlePanes) {
	d := m.idle
	if d == nil || d.request != request || !d.loading || d.pending[id] == 0 {
		return
	}
	delete(d.pending, id)
	d.status[id] = "✓"
	if list != nil {
		if list.LastPaneKept {
			d.kept++
		}
		e := m.endpoints[id]
		for _, p := range list.Panes {
			hq := m.oyagoi != nil && m.oyagoi.Machine == id && m.oyagoi.Session == m.selectionSession(id) && m.oyagoi.Workspace == p.Workspace && m.oyagoi.Generation == p.WorkspaceGeneration
			keep := idleKeepReason(p, hq, id == m.active)
			d.entries = append(d.entries, idleEntry{machine: id, label: e.profile.Label, server: list.ServerGeneration, pane: p, selected: keep == "", keep: keep})
		}
	} else if e := m.endpoints[id]; e != nil {
		d.failures = append(d.failures, idleFailure{e.profile.Label, "invalid response"})
		d.status[id] = "✗"
	}
	if len(d.pending) == 0 {
		d.loading = false
		sort.SliceStable(d.entries, func(i, j int) bool { return d.entries[i].pane.AgeNanos > d.entries[j].pane.AgeNanos })
	}
}

func idleInitiallySelected(p endpoint.IdlePane, hq, activeMachine bool) bool {
	return idleKeepReason(p, hq, activeMachine) == ""
}

// idleKeepReason says why a pane starts unchecked, in the words the dialog
// shows next to it. An empty reason means the pane starts checked.
func idleKeepReason(p endpoint.IdlePane, hq, activeMachine bool) string {
	switch {
	case hq:
		return "HQ"
	case p.Focused && activeMachine:
		return "今見ている"
	case p.State != "idle" && p.State != "done":
		return "動作中・確認待ち（" + p.State + "）"
	case !p.InputKnown:
		return "入力欄を読めない"
	case p.PendingInput:
		return "入力途中の文字がある"
	}
	return ""
}

// selectedMemory sums the RSS measured for the checked panes and counts the
// checked panes with no measurement (an older server or a failed sample).
func (d *idleDialog) selectedMemory() (kib int64, unknown int) {
	for _, i := range d.visible() {
		if e := d.entries[i]; e.selected {
			kib += e.pane.MemoryKiB
			if e.pane.MemoryKiB <= 0 {
				unknown++
			}
		}
	}
	return kib, unknown
}

// memoryText states the checked panes' RSS sum as an estimate: shared pages
// count once per process, and unmeasured panes are named, not taken as 0.
func (d *idleDialog) memoryText() string {
	kib, unknown := d.selectedMemory()
	n := d.selected()
	switch {
	case n == 0:
		return ""
	case unknown == n:
		return "メモリ（RSS）: 測れなかった"
	case unknown > 0:
		return fmt.Sprintf("メモリ（RSS 合計の目安）: 約 %s · 測れなかった %d 個", idleMemory(kib), unknown)
	}
	return "メモリ（RSS 合計の目安）: 約 " + idleMemory(kib)
}

// idleTag is the short note right after a row's check box: why a pane
// started unchecked, or a warning when such a pane was checked by hand.
func idleTag(e idleEntry) string {
	short := map[string]string{"今見ている": "表示中", "入力欄を読めない": "入力不明", "入力途中の文字がある": "入力途中"}[e.keep]
	if short == "" && e.keep != "" {
		short = e.keep
		if strings.HasPrefix(e.keep, "動作中") {
			short = "動作中"
		}
	}
	switch {
	case e.selected && short != "":
		return "注意:" + short
	case e.selected:
		return ""
	case short != "":
		return "残す:" + short
	}
	return "残す"
}

func idleMemory(kib int64) string {
	if kib >= 1024*1024 {
		return fmt.Sprintf("%.1f GiB", float64(kib)/(1024*1024))
	}
	return fmt.Sprintf("%d MiB", (kib+512)/1024)
}

// progress lists each machine with its state: … waiting, ✓ answered, ✗ failed.
func (d *idleDialog) progress(machines []idleMachine) string {
	parts := make([]string, 0, len(machines))
	for _, machine := range machines {
		parts = append(parts, machine.label+" "+d.status[machine.id])
	}
	return strings.Join(parts, " · ")
}
func (d *idleDialog) visible() []int {
	var out []int
	for i := range d.entries {
		if time.Duration(d.entries[i].pane.AgeNanos) > idleThresholds[d.threshold] {
			out = append(out, i)
		}
	}
	return out
}
func (d *idleDialog) selected() int {
	n := 0
	for _, i := range d.visible() {
		if d.entries[i].selected {
			n++
		}
	}
	return n
}

// dialog renders the list. cols is the terminal width; lines are cut to fit.
func (d *idleDialog) dialog(cols int) *render.Dialog {
	box := &render.Dialog{Title: "delete idle panes", Cancel: "close", CenterTerminal: true, PreserveTitleCase: true}
	limit := max(1, cols-8)
	// Widen the box to its longest line (render.Dialog fits only the detail),
	// cutting lines that would not fit the terminal.
	fit := func(lines []string) []string {
		box.Detail = ansi.Truncate(box.Detail, limit, "…")
		box.Width = ansi.StringWidth(box.Detail) + 5
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], limit, "…")
			box.Width = max(box.Width, ansi.StringWidth(lines[i])+5)
		}
		return lines
	}
	if d.confirming {
		box.Detail = fmt.Sprintf("%d 個のペインを閉じて、中のプロセスを止めます。", d.selected())
		lines := []string{"マージとは関係ありません。", "worktree・ブランチ・ファイルは消しません。"}
		if text := d.memoryText(); text != "" {
			lines = append(lines, text)
		}
		box.Lines = fit(lines)
		box.Confirm, box.Cancel = "close panes", "back"
		return box
	}
	if d.deleting {
		box.Detail = "ペインを閉じています… " + d.progress(d.targets)
		fit(nil)
		box.NoButtons = true
		return box
	}
	if d.loading {
		box.Detail = "動いていないペインを調べています… " + d.progress(d.machines)
		fit(nil)
		box.Confirm, box.ContinueOnly = "close", true
		return box
	}
	labels := [3]string{}
	for i, label := range idleThresholdLabels {
		if i == d.threshold {
			labels[i] = "● " + label
		} else {
			labels[i] = "○ " + label
		}
	}
	visible := d.visible()
	selected := d.selected()
	box.Detail = fmt.Sprintf("止まっている時間: [%s] [%s] [%s] (tab で切替)", labels[0], labels[1], labels[2])
	lines := []string{"ペインとプロセスだけを閉じます。マージとは関係ありません。", "worktree・ブランチ・ファイルは消しません。"}
	if len(visible) == 0 {
		lines = append(lines, fmt.Sprintf("%s以上止まっているペインはありません（調べたエージェントのペイン %d 個）。", idleThresholdLabels[d.threshold], len(d.entries)+d.kept))
	} else {
		summary := fmt.Sprintf("該当 %d 個 · [x] 閉じる %d 個 · [ ] 残す %d 個", len(visible), selected, len(visible)-selected)
		lines = append(lines, summary)
		if text := d.memoryText(); text != "" {
			lines = append(lines, text)
		}
	}
	var tail []string
	if d.kept > 0 {
		tail = append(tail, fmt.Sprintf("最後の1個のペインは閉じずに残しています（%d 個）", d.kept))
	}
	if len(d.failures) > 0 {
		parts := make([]string, 0, len(d.failures))
		for _, failure := range d.failures {
			parts = append(parts, failure.label+": "+failure.reason)
		}
		tail = append(tail, "一覧を取れなかった: "+strings.Join(parts, " · "))
	}
	if len(visible) > 0 {
		e := d.entries[visible[min(d.cursor, len(visible)-1)]]
		switch {
		case e.selected && e.keep != "":
			tail = append(tail, fmt.Sprintf("p%d: 閉じる。ただし %s。", e.pane.ID, e.keep))
		case e.selected:
			tail = append(tail, fmt.Sprintf("p%d: 閉じる（待機中か完了で、入力途中の文字がない）", e.pane.ID))
		case e.keep != "":
			tail = append(tail, fmt.Sprintf("p%d: 残す（%s）。space で閉じる側へ", e.pane.ID, e.keep))
		default:
			tail = append(tail, fmt.Sprintf("p%d: 残す（手で外した）。space で閉じる側へ", e.pane.ID))
		}
		tail = append(tail, "space で切替 · a で全部 · enter で閉じる前の確認へ")
	}
	if d.cursor >= len(visible) {
		d.cursor = max(0, len(visible)-1)
	}
	// The dialog shows 10 lines; the pane rows get what the notes leave.
	window := max(1, idleDialogLines-len(lines)-len(tail))
	start := max(0, d.cursor-window+1)
	end := min(len(visible), start+window)
	for _, i := range visible[start:end] {
		e := d.entries[i]
		mark := " "
		if e.selected {
			mark = "x"
		}
		pointer := " "
		if visible[d.cursor] == i {
			pointer = ">"
		}
		head := fmt.Sprintf("%s [%s] p%d", pointer, mark, e.pane.ID)
		if tag := idleTag(e); tag != "" {
			head += " [" + tag + "]"
		}
		tailText := fmt.Sprintf(" · %s · %s止まっている", e.pane.Agent, idleAge(time.Duration(e.pane.AgeNanos)))
		if e.pane.MemoryKiB > 0 {
			tailText += " · " + idleMemory(e.pane.MemoryKiB)
		}
		// Names give way first, so the check box, tag, age and memory stay readable.
		names := render.BudgetLabels([]string{e.label, e.pane.WorkspaceName + " / " + e.pane.TabName}, limit-ansi.StringWidth(head+tailText)-6)
		lines = append(lines, head+" · "+names+tailText)
	}
	box.Lines = fit(append(lines, tail...))
	if selected > 0 {
		box.Confirm = "review"
	} else {
		// Nothing to close: enter and the one button just close the dialog.
		box.Confirm, box.ContinueOnly = "close", true
	}
	return box
}
func idleAge(age time.Duration) string {
	if age >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", age/(24*time.Hour), age%(24*time.Hour)/time.Hour)
	}
	if age >= time.Hour {
		return fmt.Sprintf("%dh %dm", age/time.Hour, age%time.Hour/time.Minute)
	}
	return fmt.Sprintf("%dm", age/time.Minute)
}
func (m *machineClient) idleInput(ev input.Event) bool {
	d := m.idle
	if ev.Kind == input.KindMouseRelease && ev.Mouse.Button == uv.MouseLeft && !d.deleting {
		mouse := m.mouse.CellEvent(ev, m.size).Mouse
		box := d.dialog(m.size.Cols)
		_, cancel, confirm := render.DialogRects(box, layout.Rect{W: m.size.Cols, H: m.size.Rows})
		if box.ContinueOnly && contains(confirm.X, confirm.Y, confirm.W, confirm.H, mouse.X, mouse.Y) {
			// Loading or nothing to close: the only button closes the dialog.
			m.idle = nil
		} else if d.loading {
		} else if contains(cancel.X, cancel.Y, cancel.W, cancel.H, mouse.X, mouse.Y) {
			if d.confirming {
				d.confirming = false
			} else {
				m.idle = nil
			}
		} else if contains(confirm.X, confirm.Y, confirm.W, confirm.H, mouse.X, mouse.Y) {
			if d.confirming {
				m.deleteIdlePanes()
			} else if d.selected() > 0 {
				d.confirming = true
			}
		}
		return true
	}
	if ev.Kind != input.KindKey {
		return true
	}
	if ev.Key.Code == uv.KeyEscape {
		if d.confirming {
			d.confirming = false
		} else if !d.deleting {
			m.idle = nil
		}
		return true
	}
	if d.deleting {
		return true
	}
	if d.loading {
		if ev.Key.Code == uv.KeyEnter {
			m.idle = nil
		}
		return true
	}
	if d.confirming {
		if ev.Key.Code == uv.KeyEnter {
			m.deleteIdlePanes()
		}
		return true
	}
	visible := d.visible()
	switch ev.Key.Code {
	case uv.KeyTab:
		d.threshold = (d.threshold + 1) % len(idleThresholds)
		d.cursor = 0
	case uv.KeyUp:
		if d.cursor > 0 {
			d.cursor--
		}
	case uv.KeyDown:
		if d.cursor+1 < len(visible) {
			d.cursor++
		}
	case ' ':
		if len(visible) > 0 {
			e := &d.entries[visible[d.cursor]]
			e.selected = !e.selected
		}
	case 'a':
		for _, i := range visible {
			d.entries[i].selected = true
		}
	case uv.KeyEnter:
		if d.selected() > 0 {
			d.confirming = true
		} else {
			m.idle = nil
		}
	}
	return true
}
func (m *machineClient) deleteIdlePanes() {
	d := m.idle
	grouped := map[string]*endpoint.IdleDelete{}
	for _, i := range d.visible() {
		e := d.entries[i]
		if !e.selected {
			continue
		}
		if grouped[e.machine] == nil {
			grouped[e.machine] = &endpoint.IdleDelete{ServerGeneration: e.server}
		}
		grouped[e.machine].Panes = append(grouped[e.machine].Panes, e.pane)
	}
	d.confirming = false
	d.deleting = true
	d.pending = map[string]int{}
	d.targets = nil
	for _, machine := range d.machines {
		request := grouped[machine.id]
		if request == nil {
			continue
		}
		d.targets = append(d.targets, machine)
		e := m.endpoints[machine.id]
		n := len(request.Panes)
		if e == nil || e.connection == nil || !endpoint.Allows(e.connection.writer.available, endpoint.RequiredGeneration("idle_delete"), e.connection.writer.methods, "idle_delete") || e.connection.enqueue(msg{Type: "idle_delete", IdleRequest: d.request, IdleDelete: request}) != nil {
			d.failed += n
			d.status[machine.id] = "✗"
			continue
		}
		d.pending[machine.id] = n
		d.status[machine.id] = "…"
	}
	m.finishIdleDelete()
}
func (m *machineClient) idleDeleteResult(id string, request uint64, result *endpoint.IdleDeleteResult) {
	d := m.idle
	if d == nil || d.request != request || !d.deleting || d.pending[id] == 0 {
		return
	}
	n := d.pending[id]
	delete(d.pending, id)
	if result == nil || result.Deleted < 0 || result.Failed < 0 || result.Deleted+result.Failed != n {
		d.failed += n
		d.status[id] = "✗"
	} else {
		d.deleted += result.Deleted
		d.failed += result.Failed
		d.status[id] = "✓"
		if result.Failed > 0 {
			d.status[id] = "✗"
		}
	}
	m.finishIdleDelete()
}
func (m *machineClient) idleDisconnect(id string, err error) {
	d := m.idle
	if d == nil {
		return
	}
	n := d.pending[id]
	delete(d.pending, id)
	if n > 0 {
		d.status[id] = "✗"
	}
	if d.deleting {
		d.failed += n
		m.finishIdleDelete()
		return
	}
	if n > 0 {
		if e := m.endpoints[id]; e != nil {
			d.failures = append(d.failures, idleFailure{e.profile.Label, err.Error()})
		}
	}
	entries := d.entries[:0]
	for _, entry := range d.entries {
		if entry.machine != id {
			entries = append(entries, entry)
		}
	}
	d.entries = entries
	d.cursor = min(d.cursor, max(0, len(d.visible())-1))
	if d.confirming && d.selected() == 0 {
		d.confirming = false
	}
	if len(d.pending) == 0 {
		d.loading = false
		sort.SliceStable(d.entries, func(i, j int) bool { return d.entries[i].pane.AgeNanos > d.entries[j].pane.AgeNanos })
	}
}
func (m *machineClient) finishIdleDelete() {
	d := m.idle
	if d == nil || !d.deleting || len(d.pending) > 0 {
		return
	}
	m.idleToast = fmt.Sprintf("ペインを %d 個閉じた · 失敗 %d", d.deleted, d.failed)
	for i, failure := range d.failures {
		if i == 0 {
			m.idleToast += " · 一覧を取れなかった:"
		}
		m.idleToast += " " + failure.label + ": " + failure.reason
	}
	m.noticeUntil = time.Now().Add(connectionNotice)
	m.idle = nil
}
