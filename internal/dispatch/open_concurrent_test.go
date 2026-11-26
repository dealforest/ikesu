package dispatch

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestOpenForStoppingLedgerBoundsSchemaLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ikesu.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.exec("ALTER TABLE pane_sessions DROP COLUMN session_start_source"); err != nil {
		t.Fatal(err)
	}
	if err := store.exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer store.exec("ROLLBACK")

	start := time.Now()
	opened, err := OpenForStoppingLedger(path, start.Add(100*time.Millisecond))
	if opened != nil {
		opened.Close()
	}
	if !errors.Is(err, sqlite3.BUSY) || time.Since(start) > time.Second {
		t.Fatalf("locked schema open took %v and returned %v", time.Since(start), err)
	}
	if err := store.exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	opened, err = OpenForStoppingLedger(path, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	stmt, _, err := opened.db.Prepare("PRAGMA busy_timeout")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if !stmt.Step() || stmt.ColumnInt(0) != 5000 {
		t.Fatal("the shared store did not restore the regular busy timeout", stmt.Err())
	}
}

// A CLI process opens the server's live store while the server holds a write
// transaction. Open on an existing WAL database must not need the write lock,
// so every Open finishes while the transaction is still held.
func TestOpenDuringConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ikesu.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	kind, name := "agent", "server"
	writer.mu.Lock()
	if err := writer.execLocked("BEGIN IMMEDIATE"); err != nil {
		writer.mu.Unlock()
		t.Fatal(err)
	}
	if _, err := writer.upsertActorLocked(&kind, &name, nil, nil, nil, nil); err != nil {
		writer.execLocked("ROLLBACK")
		writer.mu.Unlock()
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 10; attempt++ {
				s, err := Open(path)
				if err != nil {
					failures <- fmt.Errorf("open attempt %d: %w", attempt, err)
					return
				}
				if err := s.Close(); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	// The transaction is released only after every Open returned, so each
	// Open ran while the write lock was held.
	if err := writer.execLocked("COMMIT"); err != nil {
		t.Error(err)
	}
	writer.mu.Unlock()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

// A server and CLI processes create a new session's database at the same
// time. Every Open must succeed even though only one of them can switch the
// new file to WAL mode at a time. All openers are released together so they
// race for the switch.
func TestOpenFreshDatabaseConcurrently(t *testing.T) {
	for round := 0; round < 20; round++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("r%d", round), "ikesu.db")
		var ready, wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			ready.Add(1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				ready.Done()
				<-start
				s, err := Open(path)
				if err != nil {
					errs <- err
					return
				}
				errs <- s.Close()
			}()
		}
		ready.Wait()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}
