package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/latency"
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
	setLedgerTestTimeouts(t, 100*time.Millisecond, ledgerShutdownWait)
	logFile := captureLedgerStderr(t)
	dir, done := startLedgerTestEndpoint(t)
	defer stopServer(t, dir, done)
	writer := lockTestLedger(t, "test")
	defer writer.Close()
	const id = "retry-session"
	reportAgent(t, dir, "p1", "codex", id)
	waitLedgerLog(t, api.LogPath(dir), "event=ledger.retry pane=1 stopping=false", 2, 2*time.Second)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	waitLedgerRow(t, "test", id)
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	warning := []byte("pane 1: 会話の記録がほかのプロセスにロックされています。外れるまで書き込みを待ちます")
	if count := bytes.Count(log, warning); count != 1 {
		t.Fatalf("got %d retry warnings, want 1; stderr:\n%s", count, log)
	}
}

func TestLedgerRetriesBusySharedDBInitialization(t *testing.T) {
	if runIsolatedParallelEarly(t) {
		return
	}
	teststate.Lock(t)
	s := newTestServer(t, "old-ledger")
	enableLedgerTestTrace(t)
	tracePath := filepath.Join(t.TempDir(), "trace")
	s.trace = latency.Open(tracePath)
	defer s.trace.Close()
	path := dispatch.DBPath(s.session)
	store, err := dispatch.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec("ALTER TABLE pane_sessions DROP COLUMN session_start_source"); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	unlock := lockLedgerInChild(t, path)
	defer func() {
		_ = unlock()
		s.ledgerMu.Lock()
		s.ledgerGiveUpAt = time.Now()
		if s.ledgerCancel != nil {
			s.ledgerCancel()
		}
		s.ledgerMu.Unlock()
		s.ledgerWG.Wait()
		if store := s.loadedStore(); store != nil {
			store.Close()
		}
	}()
	if err := s.app.StartInitialPane(); err != nil {
		t.Fatal(err)
	}
	defer s.sup.KillAll()
	appDone := make(chan struct{})
	go func() { s.app.Run(); close(appDone) }()
	defer func() { s.app.Stop(); <-appDone }()
	id := "init-retry-session"
	var response api.Response
	s.app.Do(func() {
		response = s.dispatchOnLoop(&api.Request{ID: "report", Method: api.MethodPaneReportAgent,
			Params: api.MarshalParams(api.PaneReportAgentParams{
				PaneID: "p1", Agent: "codex", State: "idle", Source: "test", SessionID: &id,
			})})
	})
	if response.Err != nil {
		t.Fatalf("pane.report_agent: %+v", response.Err)
	}
	// The first real initialization attempt must exhaust Open's unchanged
	// five-second SQLite timeout before we release the other process's lock.
	waitLedgerLog(t, tracePath, "event=ledger.retry pane=1 stopping=false", 1, 7*time.Second)
	if s.loadedStore() != nil {
		t.Fatal("shared store initialized before the lock was released")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	waitPromptCondition(t, func() bool { return s.loadedStore() != nil })
	waitLedgerRow(t, s.session, id)
}

func TestLedgerRetryWithoutApp(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	setLedgerTestTimeouts(t, 50*time.Millisecond, ledgerShutdownWait)
	teststate.Setenv(t, "IKESU_STATE_DIR", t.TempDir())
	logFile := captureLedgerStderr(t)
	s := &Server{session: "no-app"}
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer := lockTestLedger(t, s.session)
	defer func() {
		_ = writer.Close()
		s.ledgerWG.Wait()
	}()
	const id = "no-app-session"
	s.enqueueLedger(ledgerWrite{row: dispatch.PaneSession{Session: s.session, PaneID: 1, SessionID: id}})
	waitLedgerLog(t, logFile, "外れるまで書き込みを待ちます", 1, 2*time.Second)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	waitLedgerRow(t, s.session, id)
	s.ledgerWG.Wait()
	if err := writer.Exec("DROP TABLE pane_sessions"); err != nil {
		t.Fatal(err)
	}
	s.enqueueLedger(ledgerWrite{row: dispatch.PaneSession{Session: s.session, PaneID: 2}})
	waitLedgerLog(t, logFile, "no such table: pane_sessions", 1, 2*time.Second)
}

func TestServerShutdownRetriesLockedLedgerWrite(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	setLedgerTestTimeouts(t, 50*time.Millisecond, time.Second)
	dir, done := startLedgerTestEndpoint(t)
	writer := lockTestLedger(t, "test")
	defer func() { _ = writer.Close(); stopServer(t, dir, done) }()
	const id = "shutdown-retry-session"
	reportAgent(t, dir, "p1", "codex", id)
	apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
	waitLedgerLog(t, api.LogPath(dir), "event=ledger.retry pane=1 stopping=true", 1, 950*time.Millisecond)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	waitLedgerServerExit(t, done, writer)
	waitLedgerRow(t, "test", id)
}

func TestServerShutdownGivesUpLockedLedgerWrite(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	setLedgerTestTimeouts(t, 50*time.Millisecond, 300*time.Millisecond)
	logFile := captureLedgerStderr(t)
	dir, done := startLedgerTestEndpoint(t)
	defer stopServer(t, dir, done)
	for range 2 {
		apiOK(t, dir, api.MethodPaneSplit, api.PaneSplitParams{Direction: "right"}, nil)
	}
	writer := lockTestLedger(t, "test")
	defer writer.Close()
	for _, pane := range []string{"p1", "p2", "p3"} {
		reportAgent(t, dir, pane, "codex", "locked-"+pane)
	}
	apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
	waitLedgerServerExit(t, done, writer)
	log, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(log, []byte("session ledger shutdown deadline:")) != 1 ||
		!bytes.Contains(log, []byte("session ledger shutdown deadline: dropped 3 writes")) {
		t.Fatalf("expected one deadline log for all three writes; stderr:\n%s", log)
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

func TestLiveHandoffStartsTransferDeadlineAfterLedger(t *testing.T) {
	if teststate.RunIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	setLedgerTestTimeouts(t, 50*time.Millisecond, ledgerShutdownWait)
	original := handoffTransferWait
	handoffTransferWait = time.Second
	defer func() { handoffTransferWait = original }()
	dir, done := startLedgerTestEndpoint(t)
	writer := lockTestLedger(t, "test")
	transferred := false
	defer func() {
		_ = writer.Close()
		if transferred {
			apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
			waitAPISocketGone(t, dir)
		} else {
			stopServer(t, dir, done)
		}
	}()
	const id = "handoff-ledger-session"
	reportAgent(t, dir, "p1", "codex", id)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, rd := decisionStream(t, dir, api.MethodServerLiveHandoff, api.ServerLiveHandoffParams{ImportExe: &exe})
	defer conn.Close()
	waitLedgerLog(t, api.LogPath(dir), "event=handoff.ledger_wait", 1, 2*time.Second)
	// Hold the lock through an entire transfer budget after handoff has
	// reached the wait. An early transfer timer would now have expired.
	<-time.After(handoffTransferWait)
	if err := writer.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var response api.Response
	if err := json.NewDecoder(rd).Decode(&response); err != nil || response.Err != nil {
		t.Fatalf("live-handoff after ledger unlock: %+v %v", response.Err, err)
	}
	transferred = true
	waitLedgerServerExit(t, done, writer)
	var pong api.PongResult
	apiOK(t, dir, api.MethodPing, api.EmptyParams{}, &pong)
	if pong.Version != "test-importer" {
		t.Fatalf("expected the importer to own the API, got %+v", pong)
	}
	waitLedgerRow(t, "test", id)
}

func setLedgerTestTimeouts(t *testing.T, busy, shutdown time.Duration) {
	t.Helper()
	busyOriginal, shutdownOriginal := dispatch.LedgerBusyTimeout, ledgerShutdownWait
	dispatch.LedgerBusyTimeout, ledgerShutdownWait = busy, shutdown
	t.Cleanup(func() {
		dispatch.LedgerBusyTimeout, ledgerShutdownWait = busyOriginal, shutdownOriginal
	})
}

func enableLedgerTestTrace(t *testing.T) {
	t.Helper()
	original := latency.Enabled
	latency.Enabled = true
	t.Cleanup(func() { latency.Enabled = original })
}

func startLedgerTestEndpoint(t *testing.T) (string, chan int) {
	t.Helper()
	enableLedgerTestTrace(t)
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
	// The first jobs cleanup takes its own write lock. The expired log
	// disappears after that transaction commits.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(startupLog); os.IsNotExist(err) {
			return dir, done
		} else if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			stopServer(t, dir, done)
			t.Fatal("startup job cleanup did not remove the expired log")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitLedgerLog(t *testing.T, path, marker string, count int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte(marker)) >= count {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not observe %d occurrences of %q:\n%s", count, marker, data)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitLedgerRow(t *testing.T, session, id string) {
	t.Helper()
	reader, err := dispatch.OpenLedger(dispatch.DBPath(session))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := reader.PaneSessions(session)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].PaneID == 1 && rows[0].SessionID == id {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ledger still missing the reported session after unlock: %+v", rows)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitLedgerServerExit(t *testing.T, done <-chan int, writer *sqlite3.Conn) {
	t.Helper()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("server exit %d", code)
		}
	case <-time.After(2 * time.Second):
		// Release the fixture lock so a broken worker can finish and the
		// test process does not retain a server using the test DB.
		_ = writer.Exec("ROLLBACK")
		waitServerExit(t, done)
		t.Fatal("server did not exit before the deadline while the ledger remained locked")
	}
}

func lockTestLedger(t *testing.T, session string) *sqlite3.Conn {
	t.Helper()
	writer, err := sqlite3.Open(dispatch.DBPath(session))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Exec("PRAGMA busy_timeout=5000; BEGIN IMMEDIATE"); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	return writer
}

func lockLedgerInChild(t *testing.T, path string) func() error {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "--ledger-test-lock", path)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	unlock := sync.OnceValue(func() error {
		_ = in.Close()
		return cmd.Wait()
	})
	t.Cleanup(func() { _ = unlock() })
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatalf("external lock didn't start: %q, %v", line, unlock())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the external SQLite lock")
	}
	return unlock
}

func holdLedgerTestLock(path string) error {
	conn, err := sqlite3.Open(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	fmt.Println("locked")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		return err
	}
	return conn.Exec("COMMIT")
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
