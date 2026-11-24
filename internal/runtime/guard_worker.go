package runtime

import (
	"context"

	"github.com/kazuph/ikesu/internal/guard"
)

type guardSyncJob struct {
	pane        *Pane
	reconfigure *guard.Config
	withStarted bool
}

// ensureGuardWorker starts the worker with guardWorkMu held.
func (s *Supervisor) ensureGuardWorker() {
	if s.guardWorkCh == nil {
		s.guardWorkCh = make(chan struct{}, 1)
		s.guardWorkStop = make(chan struct{})
		s.guardWorkDone = make(chan struct{})
		go s.guardWorkerLoop()
	}
}

func (s *Supervisor) enqueueGuardSync(job guardSyncJob) {
	if job.pane.guard == nil {
		return
	}
	s.guardWorkMu.Lock()
	defer s.guardWorkMu.Unlock()
	if s.guardWorkClosed {
		return
	}
	s.ensureGuardWorker()
	if s.guardWorkIdle == nil {
		s.guardWorkIdle = make(chan struct{})
	}
	if s.guardPending == nil {
		s.guardPending = make(map[int]guardSyncJob)
	}
	if old := s.guardPending[job.pane.ID]; old.pane == job.pane {
		if job.reconfigure == nil {
			job.reconfigure = old.reconfigure
		}
		job.withStarted = job.withStarted || old.withStarted
	}
	s.guardPending[job.pane.ID] = job
	select {
	case s.guardWorkCh <- struct{}{}:
	default:
	}
}

func (s *Supervisor) guardWorkerLoop() {
	defer close(s.guardWorkDone)
	for {
		select {
		case <-s.guardWorkCh:
			s.drainGuardSync()
		case <-s.guardWorkStop:
			s.drainGuardSync()
			return
		}
	}
}

func (s *Supervisor) drainGuardSync() {
	for {
		s.guardWorkMu.Lock()
		batch := s.guardPending
		s.guardPending = nil
		if len(batch) == 0 {
			if s.guardWorkIdle != nil {
				close(s.guardWorkIdle)
				s.guardWorkIdle = nil
			}
			s.guardWorkMu.Unlock()
			return
		}
		s.guardWorkMu.Unlock()
		for _, job := range batch {
			p := job.pane
			warning := p.guard.Issue()
			if job.reconfigure != nil {
				_ = p.guard.Reconfigure(*job.reconfigure)
			}
			if p.syncGuardLatest(job.withStarted) {
				p.queueProcessChange()
				if p.guard.Issue() != warning {
					p.notifyChanged()
				}
			}
		}
	}
}

// FlushGuardSync waits for queued and running guard updates to finish.
func (s *Supervisor) FlushGuardSync() {
	_ = s.flushGuardSync(context.Background())
}

func (s *Supervisor) flushGuardSync(ctx context.Context) error {
	s.guardWorkMu.Lock()
	idle := s.guardWorkIdle
	s.guardWorkMu.Unlock()
	if idle == nil {
		return ctx.Err()
	}
	select {
	case <-idle:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopGuardWorker closes admission, drains pending updates, and joins the worker.
func (s *Supervisor) stopGuardWorker() {
	s.guardWorkMu.Lock()
	if !s.guardWorkClosed {
		s.guardWorkClosed = true
		if s.guardWorkStop != nil {
			close(s.guardWorkStop)
		}
	}
	done := s.guardWorkDone
	s.guardWorkMu.Unlock()
	if done != nil {
		<-done
	}
}
