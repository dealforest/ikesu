package server

import (
	"encoding/gob"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/teststate"
	"golang.org/x/sys/unix"
)

func TestLiveHandoffContinuesAfterFinalNoticeDisconnect(t *testing.T) {
	if runIsolatedParallel(t) {
		return
	}
	teststate.Lock(t)
	dir, done := startTestEndpointWithSetup(t, endpoint.Current(), wallReceiptClock{}, func() {
		teststate.Setenv(t, "SHELL", "/bin/bash")
	})

	transferred := false
	defer func() {
		if transferred {
			apiOK(t, dir, api.MethodServerStop, api.EmptyParams{}, nil)
			waitAPISocketGone(t, dir)
		} else {
			stopServer(t, dir, done)
		}
	}()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcherDir := t.TempDir()
	importer := filepath.Join(launcherDir, "importer")
	if err := os.Link(exe, importer); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(launcherDir, "disconnect-final")
	script := fmt.Sprintf("#!/bin/sh\nexec %q --test-drop-handoff-final \"$@\"\n", importer)
	if err := os.WriteFile(launcher, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	shell := filepath.Join(dir, "guard", "shell", "p_1")
	previous, err := os.Readlink(filepath.Join(shell, "current"))
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the initial sweep before seeding a generation that only the
	// importer's publication should discover and prune.
	sweep, err := os.OpenFile(filepath.Join(shell, "generation-prune.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sweep.Close()
	if err := unix.Flock(int(sweep.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	expired := filepath.Join(shell, "generation-test-expired")
	if err := os.Mkdir(expired, 0700); err != nil {
		t.Fatal(err)
	}
	aged := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(expired, aged, aged); err != nil {
		t.Fatal(err)
	}
	if err := sweep.Close(); err != nil {
		t.Fatal(err)
	}

	var original, first, second api.PongResult
	apiOK(t, dir, api.MethodPing, api.EmptyParams{}, &original)
	apiOK(t, dir, api.MethodServerLiveHandoff, api.ServerLiveHandoffParams{ImportExe: &launcher}, nil)
	transferred = true
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("committed sender exited with code %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not retire after receiving running")
	}
	apiOK(t, dir, api.MethodPing, api.EmptyParams{}, &first)
	if first.Generation == "" || first.Generation == original.Generation {
		t.Fatalf("first importer does not own the API: %+v", first)
	}
	deadline := time.Now().Add(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Lstat(expired); os.IsNotExist(err) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("generation pruning did not resume after the final notice was lost")
		}
		<-ticker.C
	}
	if _, err := os.ReadFile(filepath.Join(shell, previous, "bash-env.current")); err != nil {
		t.Fatalf("sender's startup payload is unavailable after pruning: %v", err)
	}

	apiOK(t, dir, api.MethodServerLiveHandoff, api.ServerLiveHandoffParams{ImportExe: &exe}, nil)
	apiOK(t, dir, api.MethodPing, api.EmptyParams{}, &second)
	if second.Generation == "" || second.Generation == first.Generation {
		t.Fatalf("second importer does not own the API: %+v", second)
	}
	t.Log("sender received running, cleanup resumed, and a second live handoff acquired the API")
}

// The relay forwards the real sender's protocol through running, then drops
// the control connection before forwarding any final notice to the importer.
// The single-pane test supplies one PTY after the control/listener/lock FDs.
func relayHandoffUntilRunning(args []string) error {
	senderFD := os.NewFile(3, "sender")
	sender, err := net.FileConn(senderFD)
	senderFD.Close()
	if err != nil {
		return err
	}
	defer sender.Close()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	local := os.NewFile(uintptr(pair[0]), "relay")
	remote := os.NewFile(uintptr(pair[1]), "importer")
	defer remote.Close()
	importer, err := net.FileConn(local)
	local.Close()
	if err != nil {
		return err
	}
	defer importer.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{remote}
	for fd := uintptr(4); fd <= 7; fd++ {
		f := os.NewFile(fd, "inherited")
		defer f.Close()
		cmd.ExtraFiles = append(cmd.ExtraFiles, f)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	remote.Close()
	handedOff := false
	defer func() {
		if handedOff {
			_ = cmd.Process.Release()
		} else {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	send, receive := gob.NewEncoder(sender), gob.NewDecoder(sender)
	push, pull := gob.NewEncoder(importer), gob.NewDecoder(importer)
	if err := relayHandoffMessage[handoffIdentity](pull, send); err != nil {
		return err
	}
	if err := relayHandoffMessage[handoffState](receive, push); err != nil {
		return err
	}
	if err := relayHandoffMessage[handoffResult](pull, send); err != nil {
		return err
	}
	if err := relayHandoffMessage[handoffResult](receive, push); err != nil {
		return err
	}
	if err := relayHandoffMessage[handoffResult](pull, send); err != nil {
		return err
	}
	handedOff = true
	return nil
}

func relayHandoffMessage[T any](from *gob.Decoder, to *gob.Encoder) error {
	var msg T
	if err := from.Decode(&msg); err != nil {
		return err
	}
	return to.Encode(msg)
}
