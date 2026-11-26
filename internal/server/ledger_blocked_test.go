package server

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/teststate"
	"github.com/kazuph/ikesu/internal/tui"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"golang.org/x/sys/unix"
)

func TestLedgerSQLiteLockDoesNotBlockDecisionOrPaneInput(t *testing.T) {
	teststate.Lock(t)
	s := newTestServer(t, "ledger-decision")
	if err := s.app.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	defer s.sup.KillAll()
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := sqlite3.Open(dispatch.DBPath(s.session))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	appDone := make(chan struct{})
	go func() { s.app.Run(); close(appDone) }()
	defer func() {
		if locked {
			_ = writer.Exec("ROLLBACK")
		}
		s.ledgerWG.Wait()
		s.app.Stop()
		<-appDone
	}()
	pid := s.app.State().Workspaces[0].Tabs[0].Layout.Focused()
	s.app.Do(func() {
		s.ledgerPut(pid, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "session", Source: "report:test"})
	})
	deadline := time.Now().Add(time.Second)
	for {
		buf := make([]byte, 1<<20)
		n := goruntime.Stack(buf, true)
		if bytes.Contains(buf[:n], []byte("(*Store).PutPaneSession")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ledger writer did not enter SQLite")
		}
		goruntime.Gosched()
	}
	s.dir, err = os.MkdirTemp("/tmp", "ikesu-ledger-api-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s.dir)
	listener, err := net.Listen("unix", api.APISocketPath(s.dir))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			s.apiWG.Add(1)
			s.serveAPI(conn)
		}
	}()
	decisionDone := make(chan api.Response, 1)
	go func() {
		decisionDone <- apiReq(t, s.dir, api.MethodDecisionGet, api.DecisionTargetParams{DecisionID: "missing"})
	}()
	decisionReady := false
	deadline = time.Now().Add(time.Second)
	for {
		select {
		case response := <-decisionDone:
			decisionReady = true
			if response.Err == nil || response.Err.Code != api.ErrDecisionNotFound {
				t.Errorf("unexpected decision.get response: %+v", response.Err)
			}
		default:
		}
		buf := make([]byte, 1<<20)
		n := goruntime.Stack(buf, true)
		if decisionReady || bytes.Contains(buf[:n], []byte("(*Store).Decisions")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("decision.get did not reach the store")
		}
		goruntime.Gosched()
	}
	path := filepath.Join(t.TempDir(), "input-seen")
	go s.app.Do(func() { s.sup.Input(pid, []byte(fmt.Sprintf("printf ready > %q\n", path))) })
	inputReady := false
	until := time.After(time.Second)
	for !decisionReady || !inputReady {
		select {
		case response := <-decisionDone:
			decisionReady = true
			if response.Err == nil || response.Err.Code != api.ErrDecisionNotFound {
				t.Errorf("unexpected decision.get response: %+v", response.Err)
			}
		default:
		}
		if _, err := os.Stat(path); err == nil {
			inputReady = true
		}
		select {
		case <-until:
			goto release
		default:
		}
		goruntime.Gosched()
	}
release:
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	locked = false
	if !decisionReady || !inputReady {
		t.Fatalf("SQLite lock blocked decision=%v or pane input=%v", !decisionReady, !inputReady)
	}
}

func TestLedgerWriteDoesNotBlockAppOnSQLiteLock(t *testing.T) {
	teststate.Lock(t)
	s := newTestServer(t, "ledger-blocked")
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := sqlite3.Open(dispatch.DBPath(s.session))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	go func() {
		s.ledgerPut(1, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "session", Source: "report:test"})
		close(finished)
	}()
	var blocked bool
	select {
	case <-finished:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	<-finished
	s.ledgerWG.Wait()
	if blocked {
		t.Fatal("ledgerPut waited for SQLite while on the app loop")
	}
	row, ok := s.ledgerEntry(1)
	if !ok || row.SessionID != "session" {
		t.Fatalf("write after unlock: %+v %v", row, ok)
	}
}

func TestLedgerSQLiteLockDoesNotStopOtherPaneInput(t *testing.T) {
	teststate.Lock(t)
	s := newTestServer(t, "ledger-input")
	if err := s.app.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	defer s.sup.KillAll()
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := sqlite3.Open(dispatch.DBPath(s.session))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	appDone := make(chan struct{})
	go func() { s.app.Run(); close(appDone) }()
	defer func() { s.app.Stop(); <-appDone }()
	pid := s.app.State().Workspaces[0].Tabs[0].Layout.Focused()
	path := filepath.Join(t.TempDir(), "input-seen")
	entered := make(chan struct{})
	ledgerDone := make(chan struct{})
	go func() {
		defer close(ledgerDone)
		s.app.Do(func() {
			close(entered)
			s.ledgerPut(pid, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "session", Source: "report:test"})
		})
	}()
	<-entered
	go s.app.Do(func() { s.sup.Input(pid, []byte(fmt.Sprintf("printf ready > %q\n", path))) })
	deadline := time.After(time.Second)
	seen := false
	for !seen {
		if _, err := os.Stat(path); err == nil {
			seen = true
			break
		}
		select {
		case <-deadline:
			goto unlocked
		case <-time.After(time.Millisecond):
		}
	}
unlocked:
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	<-ledgerDone
	s.ledgerWG.Wait()
	if !seen {
		t.Fatal("pane input did not run while SQLite was locked")
	}
}

func TestLedgerFailedWriteIsNotExposedAsSaved(t *testing.T) {
	teststate.Lock(t)
	s := newTestServer(t, "ledger-error")
	appDone := make(chan struct{})
	go func() { s.app.Run(); close(appDone) }()
	defer func() { s.app.Stop(); <-appDone }()
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer, err := sqlite3.Open(dispatch.DBPath(s.session))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec("DROP TABLE pane_sessions"); err != nil {
		t.Fatal(err)
	}
	s.ledgerPut(1, tui.AgentRecord{Agent: "codex", Kind: "id", Value: "failed", Source: "report:test"})
	s.ledgerWG.Wait()
	if row, ok := s.ledgerEntry(1); ok {
		t.Fatalf("failed write exposed: %+v", row)
	}
}

func TestServerShutdownDrainsLockedLedgerWrite(t *testing.T) {
	teststate.Lock(t)
	dir, done := startTestServer(t)
	stopped := false
	defer func() {
		if !stopped {
			stopServer(t, dir, done)
		}
	}()
	st, err := dispatch.Open(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sqlite3.Open(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// The server may still be writing its own startup rows; wait for them like
	// the server's connections do instead of failing at once with "database is
	// locked", which happened under full-suite load.
	if err := writer.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = writer.Exec("ROLLBACK")
		}
	}()
	id := "shutdown-session"
	apiOK(t, dir, api.MethodPaneReportAgent, api.PaneReportAgentParams{PaneID: "p1", Agent: "codex", State: "idle", Source: "test", SessionID: &id}, nil)
	apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	locked = false
	select {
	case code := <-done:
		stopped = true
		if code != 0 {
			t.Fatalf("server exit %d", code)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("server did not drain ledger and exit")
	}
	st, err = dispatch.Open(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.PaneSessions("test")
	if err != nil || len(rows) != 1 || rows[0].SessionID != id {
		t.Fatalf("ledger after shutdown: %+v %v", rows, err)
	}
}

func TestLedgerWriteRetriesPastBusyTimeout(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	defer func(original time.Duration) { dispatch.LedgerBusyTimeout = original }(dispatch.LedgerBusyTimeout)
	dispatch.LedgerBusyTimeout = 100 * time.Millisecond

	logFile := captureLedgerStderr(t)
	dir, done := startTestServer(t)
	defer stopServer(t, dir, done)
	writer := lockTestLedger(t, dir)
	defer writer.Close()
	const id = "retry-session"
	reportAgent(t, dir, "p1", "codex", id)
	// Keep the real write lock through multiple SQLite attempts.
	time.Sleep(time.Second)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}

	reader, err := dispatch.OpenLedger(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := reader.PaneSessions("test")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].PaneID == 1 && rows[0].SessionID == id {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ledger still missing the reported session after unlock: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	warning := []byte("pane 1: 会話の記録がほかのプロセスにロックされています。外れるまで書き込みを待ちます")
	if count := bytes.Count(log, warning); count != 1 {
		t.Fatalf("got %d retry warnings, want 1; stderr:\n%s", count, log)
	}
}

func TestServerShutdownGivesUpLockedLedgerWrite(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	defer func(shutdownWait, busyTimeout time.Duration) {
		ledgerShutdownWait, dispatch.LedgerBusyTimeout = shutdownWait, busyTimeout
	}(ledgerShutdownWait, dispatch.LedgerBusyTimeout)
	ledgerShutdownWait = 300 * time.Millisecond
	dispatch.LedgerBusyTimeout = 50 * time.Millisecond

	var startupLog string
	dir, done := startTestEndpointWithSetup(t, endpoint.Current(), wallReceiptClock{}, func() {
		store, err := dispatch.Open(dispatch.DBPath("test"))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		logDir := dispatch.JobLogDir("test")
		if err := os.MkdirAll(logDir, 0700); err != nil {
			t.Fatal(err)
		}
		startupLog = filepath.Join(logDir, "expired.log")
		if err := os.WriteFile(startupLog, nil, 0600); err != nil {
			t.Fatal(err)
		}
		expired := time.Now().Add(-2 * dispatch.JobRetention)
		if err := os.Chtimes(startupLog, expired, expired); err != nil {
			t.Fatal(err)
		}
	})
	stopRequested := false
	defer func() {
		if !stopRequested {
			stopServer(t, dir, done)
		}
	}()
	// The jobs loop takes a write lock at startup and must finish that
	// transaction first. Its cleanup of the expired log marks completion.
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(startupLog); os.IsNotExist(err) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("startup job cleanup did not remove the expired log")
		}
		time.Sleep(10 * time.Millisecond)
	}
	writer := lockTestLedger(t, dir)
	defer writer.Close()
	reportAgent(t, dir, "p1", "codex", "shutdown-locked-session")
	apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
	stopRequested = true
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("server exit %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not exit before the deadline while the ledger remained locked")
		if err := writer.Exec("ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		waitServerExit(t, done)
		return
	}
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	reader, err := dispatch.OpenLedger(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	rows, err := reader.PaneSessions("test")
	if err != nil || len(rows) != 0 {
		t.Fatalf("ledger after shutdown: %+v %v", rows, err)
	}
}

func lockTestLedger(t *testing.T, dir string) *sqlite3.Conn {
	t.Helper()
	// Open the server's shared store before locking, so contention is on the
	// pane-session write and not on schema initialization.
	res := apiReq(t, dir, api.MethodDecisionGet, api.DecisionTargetParams{DecisionID: "missing"})
	if res.Err == nil || res.Err.Code != api.ErrDecisionNotFound {
		t.Fatalf("could not initialize server store: %+v", res)
	}
	writer, err := sqlite3.Open(dispatch.DBPath("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Exec("PRAGMA busy_timeout=5000; BEGIN IMMEDIATE"); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	return writer
}

func captureLedgerStderr(t *testing.T) string {
	t.Helper()
	logFile, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	fd := int(os.Stderr.Fd())
	original, err := unix.Dup(fd)
	if err != nil {
		t.Fatal(err)
	}
	unix.CloseOnExec(original)
	t.Cleanup(func() {
		defer unix.Close(original)
		if err := unix.Dup2(original, fd); err != nil {
			t.Error(err)
		}
	})
	if err := unix.Dup2(int(logFile.Fd()), fd); err != nil {
		t.Fatal(err)
	}
	return logFile.Name()
}
