package server

// Exact per-pane agent session restore (SPEC G3): observation of the
// session id a pane's agent is actually running (explicit report wins,
// then session-file match, then cmdline resume-shape), the pane-session
// ledger, the agent.restore plan, and the missing-session warnings used
// by the restart confirmation dialog. No path ever falls back to
// "--last", "resume --last", or a cwd/mtime-latest guess.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/session"
	"github.com/kazuph/ikesu/internal/tui"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// resumeSubmitDelay separates typing the resume command from the Enter
// keypress so the receiving shell has finished its prompt draw.
const resumeSubmitDelay = 150 * time.Millisecond

// --- ledger --------------------------------------------------------------

// ledgerEntry returns the ledger row for a pane in this session. When
// the DB file does not exist no ledger rows can exist either; avoid
// opening the store so reads stay free of the engine's memory cost.
func (s *Server) ledgerEntry(pid int) (dispatch.PaneSession, bool) {
	for _, row := range s.ledgerRows() {
		if row.PaneID == pid {
			return row, true
		}
	}
	return dispatch.PaneSession{}, false
}

func (s *Server) ledgerEntries() map[int]dispatch.PaneSession {
	rows := s.ledgerRows()
	result := make(map[int]dispatch.PaneSession, len(rows))
	for _, row := range rows {
		result[row.PaneID] = row
	}
	return result
}

func (s *Server) ledgerRows() []dispatch.PaneSession {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	rows := make([]dispatch.PaneSession, 0, len(s.ledgerRowsMap))
	for _, row := range s.ledgerRowsMap {
		rows = append(rows, row)
	}
	return rows
}

// loadLedger runs before the app loop starts.
func (s *Server) loadLedger() {
	if !dispatch.DBExists(s.session) {
		return
	}
	st, err := s.db()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ikesu: load session ledger: %v\n", err)
		return
	}
	rows, err := st.PaneSessions(s.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ikesu: load session ledger: %v\n", err)
		return
	}
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerRowsMap == nil {
		s.ledgerRowsMap = map[int]dispatch.PaneSession{}
	}
	for _, row := range rows {
		s.ledgerRowsMap[row.PaneID] = row
	}
}

// ledgerPut writes the pane's session record into the ledger. Call on the
// app loop (reads model state).
func (s *Server) ledgerPut(pid int, rec tui.AgentRecord) {
	stt := s.app.State()
	wsIdx, tabIdx := stt.FindPane(pid)
	wsID, tabID := 0, 0
	cwd := ""
	if wsIdx >= 0 {
		ws := stt.Workspaces[wsIdx]
		wsID = ws.ID
		if tabIdx >= 0 && tabIdx < len(ws.Tabs) {
			tabID = ws.Tabs[tabIdx].ID
		}
		cwd = ws.Cwd
	}
	if rp := s.sup.Get(pid); rp != nil {
		if c := rp.Snapshot().Cwd; c != "" {
			cwd = c
		}
	}
	s.enqueueLedger(ledgerWrite{row: dispatch.PaneSession{
		Session:            s.session,
		PaneID:             pid,
		TerminalID:         fmtPaneID(pid),
		WorkspaceID:        wsID,
		TabID:              tabID,
		Cwd:                cwd,
		Agent:              rec.Agent,
		SessionKind:        rec.Kind,
		SessionID:          rec.Value,
		Title:              rec.Title,
		Source:             rec.Source,
		SessionStartSource: rec.SessionStartSource,
		ObservedAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}})
}

// ledgerDel drops the ledger row for a permanently closed pane.
func (s *Server) ledgerDel(pid int) {
	delete(s.observedStart, pid)
	delete(s.agyScans, pid)
	delete(s.agyObservedGroup, pid)
	s.enqueueLedger(ledgerWrite{row: dispatch.PaneSession{PaneID: pid}, delete: true})
}

type ledgerWrite struct {
	row        dispatch.PaneSession
	delete     bool
	generation uint64
}

var ledgerShutdownWait = 30 * time.Second

const ledgerInitialBackoff = 50 * time.Millisecond

func (s *Server) enqueueLedger(w ledgerWrite) {
	s.ledgerMu.Lock()
	if !s.ledgerGiveUpAt.IsZero() && !time.Now().Before(s.ledgerGiveUpAt) {
		s.ledgerMu.Unlock()
		logLedgerShutdownDrop(1)
		return
	}
	if s.ledgerVersion == nil {
		s.ledgerVersion = map[int]uint64{}
	}
	s.ledgerVersion[w.row.PaneID]++
	w.generation = s.ledgerVersion[w.row.PaneID]
	delete(s.ledgerRowsMap, w.row.PaneID)
	s.ledgerQueue = append(s.ledgerQueue, w)
	if !s.ledgerRunning {
		s.ledgerRunning = true
		s.ledgerIdle = make(chan struct{})
		s.ledgerWG.Add(1)
		ctx := context.Background()
		if s.ledgerGiveUpAt.IsZero() {
			ctx, s.ledgerCancel = context.WithCancel(ctx)
		} else {
			ctx, s.ledgerCancel = context.WithDeadline(ctx, s.ledgerGiveUpAt)
		}
		go s.runLedgerWrites(ctx, s.ledgerCancel)
	}
	s.ledgerMu.Unlock()
}

func (s *Server) runLedgerWrites(ctx context.Context, cancel context.CancelFunc) {
	defer s.ledgerWG.Done()
	defer cancel()
	var ledgerStore *dispatch.Store
	defer func() {
		if ledgerStore != nil {
			ledgerStore.Close()
		}
	}()
	backoff := ledgerInitialBackoff
	for {
		s.ledgerMu.Lock()
		if len(s.ledgerQueue) == 0 {
			s.finishLedgerWritesLocked()
			s.ledgerMu.Unlock()
			return
		}
		w := s.ledgerQueue[0]
		s.ledgerQueue[0] = ledgerWrite{}
		s.ledgerQueue = s.ledgerQueue[1:]
		s.ledgerMu.Unlock()

		var retried bool
		for {
			s.ledgerMu.Lock()
			giveUpAt := s.ledgerGiveUpAt
			expired := !giveUpAt.IsZero() && !time.Now().Before(giveUpAt)
			if retried && expired {
				count := 1 + len(s.ledgerQueue)
				s.finishLedgerWritesLocked()
				s.ledgerMu.Unlock()
				logLedgerShutdownDrop(count)
				return
			}
			superseded := s.ledgerVersion[w.row.PaneID] != w.generation
			s.ledgerMu.Unlock()
			if retried && superseded {
				break
			}

			var err error
			ledgerStore, err = s.writeLedger(ledgerStore, w, giveUpAt)
			if err == nil {
				s.recordLedgerWrite(w)
				backoff = ledgerInitialBackoff
				break
			}
			if !errors.Is(err, sqlite3.BUSY) {
				fmt.Fprintf(os.Stderr, "ikesu: save session ledger pane %d: %v\n", w.row.PaneID, err)
				s.ledgerToast(fmt.Sprintf("pane %d: %v", w.row.PaneID, err), "error")
				break
			}

			s.ledgerMu.Lock()
			giveUpAt = s.ledgerGiveUpAt
			if !giveUpAt.IsZero() && !time.Now().Before(giveUpAt) {
				count := 1 + len(s.ledgerQueue)
				s.finishLedgerWritesLocked()
				s.ledgerMu.Unlock()
				logLedgerShutdownDrop(count)
				return
			}
			superseded = s.ledgerVersion[w.row.PaneID] != w.generation
			s.ledgerMu.Unlock()
			if superseded {
				break
			}

			s.trace.Event("ledger.retry", " pane=", w.row.PaneID, " stopping=", !giveUpAt.IsZero())
			if !retried {
				message := fmt.Sprintf("pane %d: 会話の記録がほかのプロセスにロックされています。外れるまで書き込みを待ちます", w.row.PaneID)
				fmt.Fprintf(os.Stderr, "ikesu: %s\n", message)
				s.ledgerToast(message, "warning")
				retried = true
			}
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
			}
			backoff = min(2*backoff, time.Second)
		}
	}
}

func (s *Server) finishLedgerWritesLocked() {
	s.ledgerQueue = nil
	s.ledgerRunning = false
	s.ledgerCancel = nil
	close(s.ledgerIdle)
}

func logLedgerShutdownDrop(count int) {
	fmt.Fprintf(os.Stderr, "ikesu: session ledger shutdown deadline: dropped %d writes\n", count)
}

func (s *Server) writeLedger(store *dispatch.Store, w ledgerWrite, giveUpAt time.Time) (*dispatch.Store, error) {
	if store == nil {
		if _, err := s.openDB(giveUpAt); err != nil {
			return nil, err
		}
		var err error
		store, err = dispatch.OpenLedger(dispatch.DBPath(s.session))
		if err != nil {
			return nil, err
		}
	}
	if !giveUpAt.IsZero() {
		// SQLite can block in the OS lock call. Limit each new attempt
		// to the remaining budget as well as bounding the retry timer.
		timeout := max(0, min(dispatch.LedgerBusyTimeout, time.Until(giveUpAt)))
		if err := store.SetLedgerBusyTimeout(timeout); err != nil {
			return store, err
		}
	}
	if w.delete {
		return store, store.DeletePaneSession(s.session, w.row.PaneID)
	}
	return store, store.PutPaneSession(w.row)
}

func (s *Server) recordLedgerWrite(w ledgerWrite) {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgerVersion[w.row.PaneID] != w.generation {
		return
	}
	if w.delete {
		delete(s.ledgerRowsMap, w.row.PaneID)
		return
	}
	if s.ledgerRowsMap == nil {
		s.ledgerRowsMap = map[int]dispatch.PaneSession{}
	}
	s.ledgerRowsMap[w.row.PaneID] = w.row
}

func (s *Server) ledgerToast(message, kind string) {
	if app := s.app; app != nil {
		s.enqueueAppCleanup(func() { app.ShowToast("session ledger", message, kind, -1) })
	}
}

// --- session id resolution -------------------------------------------------

// paneSessionView merges the terminal-side record and the ledger row for
// one pane. Values are kept raw; `safe` marks which source produced a
// usable id and `reason` carries the warning wording when none did.
type paneSessionView struct {
	agent    string
	kind     string
	value    string
	title    string
	safe     bool
	reason   string // "invalid terminal session id" | "invalid ledger session id" | "missing session id"
	terminal bool   // terminal-side record exists
	ledger   bool   // same-agent ledger row exists
}

// resolvePaneSession computes the pane's effective session ref. Call on
// the app loop.
func (s *Server) resolvePaneSession(pid int) paneSessionView {
	var v paneSessionView
	rec, hasRec := s.app.SessionRecord(pid)
	led, hasLed := s.ledgerEntry(pid)
	v.terminal = hasRec
	v.ledger = hasLed && (!hasRec || led.Agent == "" || led.Agent == rec.Agent)

	agent := ""
	if hasRec {
		agent = rec.Agent
	}
	if agent == "" && hasLed {
		agent = led.Agent
	}
	if agent == "" {
		if rp := s.sup.Get(pid); rp != nil {
			agent = rp.Snapshot().AgentID
		}
	}
	v.agent = agent

	// Terminal record wins; a same-agent ledger row is the fallback that
	// keeps the exact id alive across a restarted terminal.
	if hasRec {
		v.kind, v.value, v.title = rec.Kind, rec.Value, rec.Title
		if session.SafeSessionValue(rec.Kind, rec.Value) {
			v.safe = true
			if v.agent == "codex" && v.kind == "id" && s.codexSessionIDTaken(pid, v.value) {
				v.safe, v.reason = false, "duplicate session id"
			}
			return v
		}
	}
	if hasLed && led.Agent == agent {
		if v.value == "" && led.SessionID != "" {
			v.kind, v.value = led.SessionKind, led.SessionID
		}
		if v.title == "" {
			v.title = led.Title
		}
		if session.SafeSessionValue(led.SessionKind, led.SessionID) {
			v.kind, v.value = led.SessionKind, led.SessionID
			v.safe = true
			if v.agent == "codex" && v.kind == "id" && s.codexSessionIDTaken(pid, v.value) {
				v.safe, v.reason = false, "duplicate session id"
			}
			return v
		}
	}
	switch {
	case hasRec && rec.Value != "":
		v.reason = "invalid terminal session id"
	case hasLed && led.Agent == agent && led.SessionID != "":
		v.reason = "invalid ledger session id"
	default:
		v.reason = "missing session id"
	}
	return v
}

// --- observation -----------------------------------------------------------

// observeAgentSessions fills missing session records for panes running a
// known agent. Call on the app loop.
func (s *Server) observeAgentSessions() {
	stt := s.app.State()
	for _, ws := range stt.Workspaces {
		for _, t := range ws.Tabs {
			for _, pid := range t.Layout.Order() {
				s.observePaneSession(pid)
			}
		}
	}
}

// observePaneSession records the pane's running agent session id when it
// can be determined exactly: explicit reports win; otherwise a unique
// session-file match (cwd + start-time window); otherwise the process's
// own resume-shape cmdline. Anything ambiguous stays unrecorded.
func (s *Server) observePaneSession(pid int) {
	observation, ok := s.prepareSessionObservation(pid)
	if !ok || observation.snapshot.ScreenAgent() == "agy" {
		return
	}
	observation.observe()
	s.applySessionObservation(observation)
}

type agyScanKey struct {
	pane      *runtime.Pane
	revision  uint64
	fg        int
	inputSeq  uint64
	record    tui.AgentRecord
	hasRecord bool
}

type agyScanEntry struct {
	key   agyScanKey
	retry session.AgyScanRetry
}

type sessionObservation struct {
	pid           int
	pane          *runtime.Pane
	snapshot      runtime.Snapshot
	record        tui.AgentRecord
	hasRecord     bool
	previousStart time.Time
	previousGroup int
	forceRecheck  bool
	start         time.Time
	startKnown    bool
	processGroup  int
	agyKey        agyScanKey
	result        tui.AgentRecord
	found         bool
	agyFailed     bool
}

func (s *Server) prepareSessionObservations() []sessionObservation {
	var observations []sessionObservation
	stt := s.app.State()
	for _, ws := range stt.Workspaces {
		for _, tab := range ws.Tabs {
			for _, pid := range tab.Layout.Order() {
				if observation, ok := s.prepareSessionObservation(pid); ok {
					observations = append(observations, observation)
				}
			}
		}
	}
	return observations
}

func (s *Server) prepareSessionObservation(pid int) (sessionObservation, bool) {
	rp := s.sup.Get(pid)
	if rp == nil {
		return sessionObservation{}, false
	}
	snap := rp.Snapshot()
	agent := snap.ScreenAgent()
	if agent == "" {
		return sessionObservation{}, false
	}
	rec, hasRec := s.app.SessionRecord(pid)
	if agent != "agy" && hasRec && rec.Value != "" && strings.HasPrefix(rec.Source, "report:") {
		return sessionObservation{}, false
	}
	conflict := hasRec && rec.Agent == "codex" && rec.Kind == "id" && s.codexSessionIDTaken(pid, rec.Value)
	_, inputSeq := rp.IdleActivity()
	key := agyScanKey{pane: rp, revision: snap.Revision, fg: snap.FgPID, inputSeq: inputSeq, record: rec, hasRecord: hasRec}
	if agent == "agy" {
		if prior, ok := s.agyScans[pid]; ok && prior.key == key && !prior.retry.MayScan() {
			return sessionObservation{}, false
		}
	}
	return sessionObservation{pid: pid, pane: rp, snapshot: snap, record: rec,
		hasRecord: hasRec, previousStart: s.observedStart[pid], previousGroup: s.agyObservedGroup[pid], forceRecheck: conflict, agyKey: key}, true
}

func (o *sessionObservation) observe() {
	job := o.pane.ForegroundJob()
	o.start, o.startKnown = session.ProcessStart(job)
	o.processGroup = job.ProcessGroupID
	if o.snapshot.ScreenAgent() != "agy" && !o.forceRecheck && o.startKnown && o.hasRecord && o.record.Value != "" && o.previousStart.Equal(o.start) {
		return
	}
	if o.snapshot.ScreenAgent() == "codex" {
		if id, ok := session.ObserveCmdline(job); ok {
			o.result = tui.AgentRecord{Agent: o.snapshot.ScreenAgent(), Kind: "id", Value: id, Title: o.record.Title, Source: "observed:cmdline"}
			o.found = true
			return
		}
	}
	if o.snapshot.ScreenAgent() == "agy" {
		o.agyFailed = true
		if !o.startKnown {
			return
		}
		id, state := session.ObserveAgyPresence(job, o.pane.SessionEnvironment())
		o.agyFailed = state == session.AgyPresenceUnknown
		id, source, update := session.SelectAgySession(state, id, job, o.previousStart, o.start, o.previousGroup, o.processGroup, o.record.Agent, o.record.Value, o.record.Source)
		if update {
			o.result = tui.AgentRecord{Agent: "agy", Value: id, Source: source}
			if id != "" {
				o.result.Kind = "id"
			}
			if o.record.Agent == "agy" {
				o.result.Title = o.record.Title
			}
			o.found = true
		}
		return
	}
	if o.startKnown {
		if id, found := session.ObserveProcessSession(o.snapshot.AgentID, o.snapshot.Cwd, o.start, job, o.pane.SessionEnvironment()); found {
			o.result = observedSessionRecord(o.snapshot.AgentID, id, o.record.Title, job)
			o.found = true
			return
		}
	}
	// cmdline is only a launch-shape fallback: it never overwrites a
	// file-observed or reported current session id.
	if o.hasRecord && o.record.Value != "" {
		return
	}
	if id, ok2 := session.ObserveCmdline(job); ok2 {
		source := "observed:cmdline"
		if spec, native := nativeResumeAgents[o.snapshot.ScreenAgent()]; native {
			source = spec.Source
		}
		o.result = tui.AgentRecord{Agent: o.snapshot.ScreenAgent(), Kind: "id", Value: id, Title: o.record.Title, Source: source}
		o.found = true
	}
}

func (s *Server) applySessionObservation(o sessionObservation) {
	if (!o.found && o.snapshot.ScreenAgent() != "agy") || s.sup.Get(o.pid) != o.pane {
		return
	}
	current := o.pane.Snapshot()
	rec, hasRec := s.app.SessionRecord(o.pid)
	if current.ScreenAgent() != o.snapshot.ScreenAgent() || (o.snapshot.ScreenAgent() == "agy" && current.ForegroundAgent != o.snapshot.ForegroundAgent) || current.Cwd != o.snapshot.Cwd || current.FgPID != o.snapshot.FgPID || current.Dead || hasRec != o.hasRecord || rec != o.record || !s.observedStart[o.pid].Equal(o.previousStart) {
		s.markSessionDirty()
		return
	}
	if o.result.Agent == "codex" && s.codexSessionIDTaken(o.pid, o.result.Value) {
		if hasRec && rec.Value != o.result.Value && !strings.HasPrefix(rec.Source, "report:") {
			rec.Kind, rec.Value = "", ""
			s.app.RecordSession(o.pid, rec)
			s.ledgerDel(o.pid)
		}
		return
	}
	if o.snapshot.ScreenAgent() == "agy" {
		// FD reads happen off the app loop. Reject a result if the pane has
		// output since the read began or its foreground process changed.
		job := o.pane.ForegroundJob()
		start, ok := session.ProcessStart(job)
		_, inputSeq := o.pane.IdleActivity()
		if current.Revision != o.snapshot.Revision || inputSeq != o.agyKey.inputSeq || (o.startKnown && (!ok || job.ProcessGroupID != o.processGroup || !start.Equal(o.start))) {
			s.markSessionDirty()
			return
		}
		if s.agyScans == nil {
			s.agyScans = map[int]agyScanEntry{}
		}
		entry := s.agyScans[o.pid]
		if entry.key != o.agyKey {
			entry = agyScanEntry{key: o.agyKey}
		}
		if o.agyFailed {
			if entry.retry.Fail() {
				s.markSessionDirty()
			}
		} else {
			entry.retry.Complete()
		}
		s.agyScans[o.pid] = entry
		if !o.found {
			return
		}
	}
	if o.result.Agent == "agy" && hasRec && rec == o.result {
		if o.result.Agent == "agy" && o.startKnown {
			if s.agyObservedGroup == nil {
				s.agyObservedGroup = map[int]int{}
			}
			s.agyObservedGroup[o.pid] = o.processGroup
		}
		if o.startKnown {
			if s.observedStart == nil {
				s.observedStart = map[int]time.Time{}
			}
			s.observedStart[o.pid] = o.start
		}
		return
	}
	s.app.RecordSession(o.pid, o.result)
	if o.result.Value == "" {
		s.ledgerDel(o.pid)
	} else {
		s.ledgerPut(o.pid, o.result)
	}
	if o.result.Agent == "agy" {
		key := o.agyKey
		key.record, key.hasRecord = o.result, true
		entry := s.agyScans[o.pid]
		entry.key = key
		s.agyScans[o.pid] = entry
		if s.agyObservedGroup == nil {
			s.agyObservedGroup = map[int]int{}
		}
		s.agyObservedGroup[o.pid] = o.processGroup
	}
	if o.startKnown {
		if s.observedStart == nil {
			s.observedStart = map[int]time.Time{}
		}
		s.observedStart[o.pid] = o.start
	}
}

func (s *Server) codexSessionIDTaken(pid int, id string) bool {
	for otherPID, other := range s.app.SessionRecords() {
		if otherPID != pid && other.Agent == "codex" && other.Kind == "id" && other.Value == id {
			return true
		}
	}
	for otherPID, other := range s.ledgerEntries() {
		if otherPID != pid && other.Agent == "codex" && other.SessionKind == "id" && other.SessionID == id {
			return true
		}
	}
	return false
}

// --- restore plan ----------------------------------------------------------

func (s *Server) buildResumeCommand(agent, kind, value string) (string, bool) {
	return session.ResumeCommand(s.cfg, agent, kind, value)
}

// runAgentRestore builds and optionally executes the restore plan. Call
// on the app loop. It always shows the result toast (SPEC G3).
func (s *Server) runAgentRestore(dryRun bool) api.AgentRestoreResult {
	s.observeAgentSessions()
	var actions []api.AgentRestoreActionInfo
	running := 0
	stt := s.app.State()
	for _, ws := range stt.Workspaces {
		for _, t := range ws.Tabs {
			for _, pid := range t.Layout.Order() {
				v := s.resolvePaneSession(pid)
				if rp := s.sup.Get(pid); rp != nil && rp.AgentRunningForRestore(v.agent) {
					running++
					// Never double-launch a live agent; surface the
					// per-pane skip with its spec reason.
					reason := "agent already running"
					agent := v.agent
					if rp.Snapshot().ForegroundAgent == "agy" {
						agent = "agy"
					}
					if agent == "" {
						agent = rp.Snapshot().AgentID
					}
					actions = append(actions, api.AgentRestoreActionInfo{
						PaneID: fmtPaneID(pid), Agent: agent,
						Status: "skipped", Reason: &reason,
					})
					continue
				}
				if !v.terminal && !v.ledger {
					continue // no session association: not a restore target
				}
				if v.agent == "" {
					continue
				}
				if !v.safe {
					reason := "no resumable session found"
					if v.reason == "duplicate session id" {
						reason = v.reason
					}
					actions = append(actions, api.AgentRestoreActionInfo{
						PaneID: fmtPaneID(pid), Agent: v.agent,
						Status: "skipped", Reason: &reason,
					})
					continue
				}
				if s.sup.Get(pid) == nil {
					reason := "pane process not running"
					actions = append(actions, api.AgentRestoreActionInfo{
						PaneID: fmtPaneID(pid), Agent: v.agent,
						Status: "skipped", Reason: &reason,
					})
					continue
				}
				cmd, ok := s.buildResumeCommand(v.agent, v.kind, v.value)
				if !ok {
					reason := "no resumable session found"
					actions = append(actions, api.AgentRestoreActionInfo{
						PaneID: fmtPaneID(pid), Agent: v.agent,
						Status: "skipped", Reason: &reason,
					})
					continue
				}
				act := api.AgentRestoreActionInfo{
					PaneID: fmtPaneID(pid), Agent: v.agent,
					Command: &cmd,
				}
				if dryRun {
					act.Status = "would_launch"
				} else {
					act.Status = "launched"
					s.typeResume(pid, cmd)
					if v.agent == "claude" && v.kind == "id" {
						s.watchInterruptedBackground(pid, s.sup.Get(pid), v.value, time.Now())
					}
				}
				actions = append(actions, act)
			}
		}
	}
	s.showRestoreToast(actions, running, dryRun)
	return api.AgentRestoreResult{
		Type:    "agent_restore",
		DryRun:  dryRun,
		Actions: actions,
	}
}

// agentRestore is the API handler for `agent.restore`.
func (s *Server) agentRestore(id string, p api.AgentRestoreParams) api.Response {
	return api.Success(id, s.runAgentRestore(p.DryRun))
}

// typeResume types the resume command into the pane, then submits it
// after a short delay so the shell is ready (SPEC: submit delay).
func (s *Server) typeResume(pid int, cmd string) {
	s.sup.Input(pid, []byte(cmd))
	time.AfterFunc(resumeSubmitDelay, func() {
		s.app.Do(func() { s.sup.Input(pid, []byte("\r")) })
	})
}

// showRestoreToast reports the restore outcome (SPEC G3 empty-state
// wording): distinguish "nothing pending" from "already running",
// dry-run from real execution.
func (s *Server) showRestoreToast(actions []api.AgentRestoreActionInfo, running int, dryRun bool) {
	// "agent already running" rows are informational skips, not restore
	// targets — the toast counts pending/launchable work only (SPEC G3
	// empty-state wording).
	pending := actions[:0:0]
	for _, a := range actions {
		if a.Reason != nil && *a.Reason == "agent already running" {
			continue
		}
		pending = append(pending, a)
	}
	var body string
	if len(pending) == 0 {
		body = "no pending restore"
		if running > 0 {
			body = fmt.Sprintf("no pending restore, %d already running", running)
		}
	} else {
		launched, skipped := 0, 0
		for _, a := range pending {
			switch a.Status {
			case "skipped":
				skipped++
			default:
				launched++
			}
		}
		if dryRun {
			body = fmt.Sprintf("would launch %d, skipped %d", launched, skipped)
		} else {
			body = fmt.Sprintf("launched %d, skipped %d", launched, skipped)
		}
	}
	s.app.ShowToast("agent restore", body, "info", -1)
}

// --- restart warnings --------------------------------------------------------

// agentSessionWarnings lists agent panes that would fail closed on
// restart because no safe session id is recorded (SPEC G3 fail-closed
// restart warning).
func (s *Server) agentSessionWarnings() []api.AgentSessionWarningInfo {
	s.observeAgentSessions()
	var out []api.AgentSessionWarningInfo
	stt := s.app.State()
	for wi, ws := range stt.Workspaces {
		for _, t := range ws.Tabs {
			for _, pid := range t.Layout.Order() {
				rp := s.sup.Get(pid)
				detected := ""
				if rp != nil {
					detected = rp.Snapshot().AgentID
				}
				v := s.resolvePaneSession(pid)
				isAgentPane := v.terminal || v.ledger || detected != ""
				if !isAgentPane || v.safe {
					continue
				}
				label := paneLabelOf(stt, pid)
				w := api.AgentSessionWarningInfo{
					WorkspaceNumber: wi + 1,
					WorkspaceLabel:  ws.Name,
					PaneLabel:       label,
					Agent:           v.agent,
					Cwd:             s.paneCwdOf(ws, pid),
					Reason:          v.reason,
				}
				if v.title != "" {
					w.Title = &v.title
				}
				out = append(out, w)
			}
		}
	}
	return out
}

func paneLabelOf(stt *model.AppState, pid int) string {
	if l, ok := stt.PaneLabels[pid]; ok && l != "" {
		return l
	}
	return fmtPaneID(pid)
}

// paneCwdOf resolves the pane's working directory: runtime cwd first,
// then the workspace's directory.
func (s *Server) paneCwdOf(ws *model.Workspace, pid int) string {
	if rp := s.sup.Get(pid); rp != nil {
		if c := rp.Snapshot().Cwd; c != "" {
			return c
		}
	}
	return ws.Cwd
}

// scheduleAutoResume runs the agent restore plan once, restore_delay_ms
// after startup, when [session] resume_agents_on_restore is on.
func (s *Server) scheduleAutoResume() {
	if !s.cfg.ResumeAgentsOnRestore() && !s.cfg.LegacyRestoreEnabled() {
		return
	}
	delay := time.Duration(s.cfg.RestoreDelay()) * time.Millisecond
	time.AfterFunc(delay, func() {
		s.app.Do(func() { s.runAgentRestore(false) })
	})
}
