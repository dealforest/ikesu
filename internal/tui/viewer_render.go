package tui

import (
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/render"
	"time"
)

func (v *Viewer) onInput(ui *App, event input.Event) {
	ui.resizeInput(event, func() { v.applyInput(ui, event) })
}

type viewerInput struct {
	event input.Event
	order uint64
}

func (v *Viewer) applyInput(ui *App, event input.Event) {
	root := ui.viewerOwner
	root.sizeInputOrder++
	pending := viewerInput{event, root.sizeInputOrder}
	if event.Kind == input.KindFocus {
		v.awaitFocusFrame()
	}
	if time.Now().Before(v.focusUntil) && (event.Kind == input.KindKey || event.Kind == input.KindPaste) {
		v.heldKeys = append(v.heldKeys, pending)
		return
	}
	v.deliverInput(ui, pending)
}

func (v *Viewer) awaitFocusFrame() {
	v.focusEpoch++
	v.focusUntil = time.Now().Add(time.Second) // SPEC G11: at most one second
}

// Classify the event by its actual effect on this viewer's selection.
// Focus-held input can reach the pane after another viewer's newer input.
func (v *Viewer) deliverInput(ui *App, pending viewerInput) {
	event := pending.event
	root := ui.viewerOwner
	before := ui.currentSizeTab()
	resizing := ui.resizeDragActive()
	hadPrefix := ui.prefix
	ui.onInput(event)
	after := ui.currentSizeTab()
	basis := root.sizeOwners[after]
	if after != before {
		v.sizeOnTabEntry(ui)
		return
	}
	// The leader key only arms the next command. Classify that command
	// at its own event so a prefixed switch also leaves the source alone.
	if event.Kind == input.KindKey && !hadPrefix && ui.prefix {
		return
	}
	if basis.inputOrder > pending.order {
		return
	}
	switch event.Kind {
	case input.KindKey, input.KindPaste, input.KindMouseMotion, input.KindMouseWheel, input.KindFocus:
	case input.KindMouseRelease:
		if !resizing {
			return
		}
	default:
		return
	}
	v.takeSize(root)
	basis = root.sizeOwners[after]
	basis.inputOrder = pending.order
	root.sizeOwners[after] = basis
	ui.syncLayout()
}

func (v *Viewer) releaseKeys(ui *App) {
	v.focusUntil = time.Time{}
	for _, event := range v.heldKeys {
		v.deliverInput(ui, event)
	}
	v.heldKeys = nil
	if v.receivedInput > v.processedInput {
		ui.dirty = true
	}
}

// SetSnapshotSink switches only this attached viewer to client composition.
func (v *Viewer) SetSnapshotSink(sink func(render.Input) error) {
	v.app.snapshotSink = sink
}

func (v *Viewer) Redraw(a *App) { v.with(a, func(ui *App) { ui.dirty = true }) }

func (v *Viewer) AwaitingFocusRender() bool { return !v.focusUntil.IsZero() }

func (v *Viewer) FocusEpoch() uint64 {
	if v.AwaitingFocusRender() {
		return v.focusEpoch
	}
	return 0
}

// Rendered uses the client-composed hit regions for the next input event.
// A frame drawn before the latest focus return cannot release held keys.
func (v *Viewer) Rendered(a *App, out render.Output, focusEpoch uint64) {
	if v.AwaitingFocusRender() && focusEpoch != v.focusEpoch {
		return
	}
	v.with(a, func(ui *App) {
		wsID, tabID := 0, 0
		if ws := ui.st.ActiveWorkspace(); ws != nil {
			wsID = ws.ID
			if tab := ws.TabRef(); tab != nil {
				tabID = tab.ID
			}
		}
		ui.tabViewWorkspace, ui.tabViewActive = wsID, tabID
		ui.tabViewWidth = ui.st.TabBarArea().W
		ui.tabScroll, ui.tabScrollLeft, ui.tabScrollRight = out.TabScroll, out.TabScrollLeft, out.TabScrollRight
		ui.tabLeftRect, ui.tabRightRect = out.TabLeftRect, out.TabRightRect
		ui.tabHits, ui.newTabRect = out.TabHits, out.NewTabRect
		ui.toastRect, ui.actionBar = out.ToastRect, out.ActionBar
		ui.paintedPaneViews = out.PaneViewports
		if ui.help != nil {
			ui.help.rect = out.HelpRect
		}
		v.releaseKeys(ui)
	})
}

// Input receipts advance only after framed events and focus-held keys have
// reached the viewer, so a snapshot cannot acknowledge an unhandled prefix.
func (v *Viewer) RecordInput(a *App, sequence uint64) {
	v.receivedInput = sequence
	v.Redraw(a)
}
func (v *Viewer) ProcessedInput() uint64 {
	if len(v.heldKeys) == 0 && !v.app.fr.Pending() {
		v.processedInput = v.receivedInput
	}
	return v.processedInput
}
