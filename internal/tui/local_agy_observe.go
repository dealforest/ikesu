package tui

import (
	"time"

	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/session"
)

type localAgyKey struct {
	revision  uint64
	fg        int
	inputSeq  uint64
	record    AgentRecord
	hasRecord bool
}
type localAgyScan struct {
	key   localAgyKey
	retry session.AgyScanRetry
}

type localAgyResult struct {
	pid      int
	pane     *runtime.Pane
	snapshot runtime.Snapshot
	key      localAgyKey
	group    int
	start    time.Time
	next     AgentRecord
	found    bool
	failed   bool
}

// The app loop never waits for lsof. One result per output revision is enough;
// the next terminal output or an explicit report schedules another observation.
func (a *App) queueLocalAgyObservation(pid int, pane *runtime.Pane, snap runtime.Snapshot, rec AgentRecord, hasRec bool) {
	if a.localAgyPending == nil {
		a.localAgyPending = map[int]bool{}
		a.localAgyGroup = map[int]int{}
		a.localAgySeen = map[int]localAgyScan{}
		a.localAgyResults = make(chan localAgyResult, 64)
	}
	_, inputSeq := pane.IdleActivity()
	key := localAgyKey{revision: snap.Revision, fg: snap.FgPID, inputSeq: inputSeq, record: rec, hasRecord: hasRec}
	if a.localAgyPending[pid] {
		return
	}
	if previous, ok := a.localAgySeen[pid]; ok && previous.key == key && !previous.retry.MayScan() {
		return
	}
	entry := a.localAgySeen[pid]
	if entry.key != key {
		entry = localAgyScan{key: key}
	}
	a.localAgySeen[pid] = entry
	a.localAgyPending[pid] = true
	env := pane.SessionEnvironment()
	previousStart := a.localObservedStart[pid]
	previousGroup := a.localAgyGroup[pid]
	go func() {
		result := localAgyResult{pid: pid, pane: pane, snapshot: snap, key: key, failed: true}
		job := pane.ForegroundJob()
		start, ok := session.ProcessStart(job)
		if ok {
			result.start, result.group = start, job.ProcessGroupID
			id, state := session.ObserveAgyPresence(job, env)
			result.failed = state == session.AgyPresenceUnknown
			id, source, update := session.SelectAgySession(state, id, job, previousStart, start, previousGroup, job.ProcessGroupID, rec.Agent, rec.Value, rec.Source)
			if update {
				result.next = AgentRecord{Agent: "agy", Value: id, Source: source}
				if id != "" {
					result.next.Kind = "id"
				}
				if rec.Agent == "agy" {
					result.next.Title = rec.Title
				}
				result.found = true
			}

		}
		select {
		case a.localAgyResults <- result:
		case <-a.doneCh:
		}
	}()
}

func (a *App) applyLocalAgyObservations() {
	for a.localAgyResults != nil {
		select {
		case r := <-a.localAgyResults:
			if a.sup.Get(r.pid) != r.pane {
				continue
			}
			delete(a.localAgyPending, r.pid)
			snap := r.pane.Snapshot()
			rec, hasRec := a.SessionRecord(r.pid)
			job := r.pane.ForegroundJob()
			start, ok := session.ProcessStart(job)
			_, inputSeq := r.pane.IdleActivity()
			if inputSeq != r.key.inputSeq || snap.ScreenAgent() != "agy" || snap.Revision != r.snapshot.Revision || snap.FgPID != r.snapshot.FgPID || rec != r.key.record || hasRec != r.key.hasRecord || (!r.start.IsZero() && (!ok || job.ProcessGroupID != r.group || !start.Equal(r.start))) {
				delete(a.localAgySeen, r.pid)
				continue
			}
			entry := a.localAgySeen[r.pid]
			if entry.key != r.key {
				continue
			}
			if r.failed {
				entry.retry.Fail()
			} else {
				entry.retry.Complete()
			}
			a.localAgySeen[r.pid] = entry
			if !r.found {
				continue
			}
			a.localAgyGroup[r.pid] = r.group
			if !hasRec || rec != r.next {
				a.RecordSession(r.pid, r.next)
			}
			a.localObservedStart[r.pid] = r.start
			r.key.record, r.key.hasRecord = r.next, true
			entry.key = r.key
			a.localAgySeen[r.pid] = entry
		default:
			return
		}
	}
}
