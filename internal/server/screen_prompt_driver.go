package server

import (
	"time"

	"github.com/kazuph/ikesu/internal/config"
)

// screenPromptDriver is the app-loop boundary for screen detection. A disabled
// configuration has no driver and never admits output, scans, or polling.
type screenPromptDriver interface {
	active(*Server) bool
	schedule(*Server)
	scan(*Server)
	output(*Server, int)
}

func (s *Server) screenPromptDriver() screenPromptDriver {
	if !s.cfg.ScreenPrompts() {
		return nil
	}
	return &s.screenPrompts
}

func (s *Server) screenPromptsActive() bool {
	driver := s.screenPromptDriver()
	return driver != nil && driver.active(s)
}

func (s *Server) scheduleScreenPrompts() {
	if driver := s.screenPromptDriver(); driver != nil {
		driver.schedule(s)
	}
}

func (s *Server) scanScreenPrompts() {
	if driver := s.screenPromptDriver(); driver != nil && driver.active(s) {
		driver.scan(s)
	}
}

func (s *Server) screenPromptOutput(pid int) {
	if driver := s.screenPromptDriver(); driver != nil {
		driver.output(s, pid)
	}
}

func (h *screenPromptHub) active(s *Server) bool {
	if len(h.watches) > 0 {
		return true
	}
	if len(h.verifying) > 0 {
		return true
	}
	if len(h.claudeRestore) > 0 {
		return true
	}
	for _, late := range h.late {
		if late.delivery != nil && late.delivery.Status == "pending" {
			return true
		}
	}
	active := false
	s.eachPane(func(pid, _, _ int) {
		active = active || s.screenPromptPaneActive(pid)
	})
	return active
}

func (s *Server) screenPromptPaneActive(pid int) bool {
	if s.screenPrompts.watches[pid] != nil {
		return true
	}
	p := s.sup.Get(pid)
	if p == nil || p.Snapshot().Dead {
		return false
	}
	agent := p.Snapshot().AgentID
	if reported, _, ok := s.app.Reported(pid); ok && reported != "" {
		agent = reported
	}
	return (agent == "claude" || agent == "codex" || agent == "devin" || agent == "agy") && s.app.PaneStatusKey(pid) == "blocked"
}

// Output and status hooks inspect only their pane. Reservations return before
// reading even that pane's state.
func (h *screenPromptHub) output(s *Server, pid int) {
	if h.timer != nil || !s.screenPromptPaneActive(pid) {
		return
	}
	h.armTimer(s)
}

func (s *Server) screenPromptPollInterval() time.Duration {
	for _, w := range s.screenPrompts.watches {
		if w.preview != nil {
			return eventWaitPoll
		}
	}
	return screenPromptWaitPoll
}

func (h *screenPromptHub) schedule(s *Server) {
	if h.timer != nil || !h.active(s) {
		return
	}
	h.armTimer(s)
}

func (h *screenPromptHub) armTimer(s *Server) {
	s.initDecisionHub()
	h.timerGen++
	gen := h.timerGen
	h.timer = time.AfterFunc(s.screenPromptPollInterval(), func() {
		select {
		case <-s.decisions.stopped:
			return
		default:
		}
		s.app.Do(func() { s.runScreenPromptTimer(gen) })
	})
}

// runScreenPromptTimer runs on the app loop, where reloads also change the timer.
func (s *Server) runScreenPromptTimer(gen uint64) {
	if gen != s.screenPrompts.timerGen {
		return
	}
	s.screenPrompts.timer = nil
	select {
	case <-s.decisions.stopped:
		return
	default:
	}
	s.scanScreenPrompts()
	s.scheduleScreenPrompts()
}

func (s *Server) stopScreenPromptTimer() {
	s.screenPrompts.timerGen++
	if s.screenPrompts.timer != nil {
		s.screenPrompts.timer.Stop()
		s.screenPrompts.timer = nil
	}
}

func (s *Server) screenPromptConfig(c config.Config) {
	s.cfg = c
	h := &s.screenPrompts
	if c.ScreenPrompts() {
		h.disabledCleaned = false
		s.invalidatePendingDecisions()
		s.prepareClaudeQuestionRestore()
		s.scheduleScreenPrompts()
		return
	}
	if h.disabledCleaned && len(h.watches) == 0 && len(h.verifying) == 0 && len(h.claudeRestore) == 0 && len(h.late) == 0 {
		return
	}
	h.disabledCleaned = false
	s.invalidatePendingDecisions()
	s.stopScreenPromptTimer()
	h.flows = nil
	h.seen = nil
	for id, old := range s.screenPrompts.verifying {
		if old.delivery != nil {
			s.finishVerifyingDelivery(id, old, "画面の質問の検出が無効になりました")
		}
	}
	s.screenPrompts.verifying = nil
	s.screenPrompts.claudeRestore = nil
	s.screenPrompts.claudeRestoreCaches = nil
	for id, late := range s.screenPrompts.late {
		if late.delivery != nil && late.delivery.Status == "pending" {
			s.setLateDelivery(id, "failed", "画面の質問の検出が無効になりました")
		}
	}
	for _, w := range s.screenPrompts.watches {
		s.cancelScreenPrompt(w)
	}
	s.screenPrompts.watches = nil
	// Include pending records restored before their pane's first scan.
	cleaned := true
	if s.store != nil {
		pending, err := s.pendingDecisionsCached()
		if err != nil {
			s.app.SetConfigWarning(err.Error())
			return
		}
		for _, d := range pending {
			if d.Origin != nil && d.Origin.Reason == "screen_prompt" {
				w := &screenPromptWatch{decisionID: d.DecisionID}
				s.cancelScreenPrompt(w)
				cleaned = cleaned && w.resolved
			}
		}
	}
	h.disabledCleaned = cleaned
}
