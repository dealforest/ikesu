package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/screenprompt"
)

// Whitespace differs at terminal wrapping boundaries; compare the visible
// non-whitespace characters and refuse when the editor truncates the answer.
func agyTextIdentity(s string) string { return strings.Join(strings.Fields(s), "") }

func (s *Server) deliverAgyWriteIn(pid int, signature string, prompt *screenprompt.Prompt, text string) error {
	if err := s.navigateScreenRow(pid, signature, prompt.TextID); err != nil {
		return err
	}
	var current *screenprompt.Prompt
	s.app.Do(func() { current = s.readScreenPrompt(pid) })
	if current == nil || current.Cursor < 0 || current.Cursor >= len(current.Items) || current.Items[current.Cursor] != prompt.TextID {
		return fmt.Errorf("agy write-in row changed")
	}
	if err := s.screenInputChecked(pid, signature, "\r", true, nil, &current.Cursor); err != nil {
		return err
	}
	deadline := time.Now().Add(multiSelectSettle)
	for {
		interrupted := false
		s.app.Do(func() {
			interrupted = s.screenUserActive(pid) || s.screenAnswerInterrupted(pid)
			current = s.readScreenPrompt(pid)
		})
		if interrupted {
			return fmt.Errorf("pane_user_active: external input interrupted delivery")
		}
		if current != nil && current.Signature() != signature {
			return fmt.Errorf("question changed before agy editor opened")
		}
		if current != nil && current.TextOpen {
			break
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("agy editor did not open")
		}
		timer := time.NewTimer(eventWaitPoll)
		select {
		case <-timer.C:
		case <-s.decisions.stopped:
			timer.Stop()
			return fmt.Errorf("server stopped during input delivery")
		}
	}
	open := true
	empty := ""
	if err := s.screenInputCheckedPaste(pid, signature, text, true, &open, nil, true, &empty); err != nil {
		return err
	}
	deadline = time.Now().Add(multiSelectSettle)
	for {
		interrupted := false
		s.app.Do(func() {
			interrupted = s.screenUserActive(pid) || s.screenAnswerInterrupted(pid)
			current = s.readScreenPrompt(pid)
		})
		if interrupted {
			return fmt.Errorf("pane_user_active: external input interrupted delivery")
		}
		if current == nil || current.Signature() != signature || !current.TextOpen {
			return fmt.Errorf("agy editor changed after paste")
		}
		if agyTextIdentity(current.TextValue) == agyTextIdentity(text) {
			break
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("agy editor does not show the submitted text")
		}
		timer := time.NewTimer(eventWaitPoll)
		select {
		case <-timer.C:
		case <-s.decisions.stopped:
			timer.Stop()
			return fmt.Errorf("server stopped during input delivery")
		}
	}
	return s.screenInputCheckedPaste(pid, signature, "\r", true, &open, nil, false, &text)
}
