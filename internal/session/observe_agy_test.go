package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/platform"
)

func TestAgyPresenceRealFD(t *testing.T) {
	if os.Getenv("AGY_FD_TEST_HELPER") == "1" {
		files := []*os.File{}
		for _, path := range filepath.SplitList(os.Getenv("AGY_FD_TEST_PATHS")) {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				os.Exit(2)
			}
			files = append(files, f)
		}
		fmt.Println("ready")
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		for _, f := range files {
			f.Close()
		}
		return
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".gemini", "antigravity-cli", "presence")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "agy")
	if err := os.Symlink(exe, bin); err != nil {
		t.Fatal(err)
	}
	id := "12345678-1234-1234-abcd-123456789abc"
	for _, tc := range []struct {
		name  string
		names []string
		want  bool
	}{
		{"unique", []string{id + ".lock"}, true},
		{"multiple", []string{id + ".lock", "abcdefab-1234-1234-abcd-123456789abc.lock"}, false},
		{"noncanonical", []string{"BBBBBBBB-1234-1234-ABCD-123456789abc.lock"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := make([]string, len(tc.names))
			for i, name := range tc.names {
				paths[i] = filepath.Join(root, name)
			}
			cmd := exec.Command(bin, "-test.run=^TestAgyPresenceRealFD$")
			cmd.Env = append(os.Environ(), "AGY_FD_TEST_HELPER=1", "AGY_FD_TEST_PATHS="+joinPaths(paths))
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { in.Close(); cmd.Wait() }()
			var b [6]byte
			if _, err := out.Read(b[:]); err != nil {
				t.Fatal(err)
			}
			pid := cmd.Process.Pid
			job := platform.ForegroundJob{ProcessGroupID: pid, Processes: []platform.ForegroundProcess{{PID: pid, Name: platform.ProcName(pid), Argv: platform.ProcArgs(pid)}}}
			got, state := observeAgyPresence(home, job)
			ok := state == AgyPresenceUnique
			if tc.name == "multiple" && state != AgyPresenceAmbiguous {
				t.Fatalf("state=%v", state)
			}
			if ok != tc.want || (ok && got != id) {
				t.Fatalf("got %q %v", got, ok)
			}
			if _, state := observeAgyPresence(filepath.Join(home, "elsewhere"), job); state == AgyPresenceUnique {
				t.Fatal("accepted wrong home")
			}
		})
	}
}

func joinPaths(paths []string) string {
	var s string
	for i, p := range paths {
		if i > 0 {
			s += string(os.PathListSeparator)
		}
		s += p
	}
	return s
}

func TestSelectAgySessionAcrossProcessChanges(t *testing.T) {
	old := time.Unix(100, 0)
	newStart := time.Unix(101, 0)
	job := platform.ForegroundJob{ProcessGroupID: 22, Processes: []platform.ForegroundProcess{{PID: 22, Name: "agy", Argv: []string{"agy", "--conversation", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}}}
	const first = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const second = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	cases := []struct {
		name                                   string
		state                                  AgyPresenceState
		previousStart, start                   time.Time
		previousGroup, group                   int
		previousID, source, wantID, wantSource string
		update                                 bool
	}{
		{"unreadable keeps old", AgyPresenceUnknown, old, newStart, 11, 22, first, "observed:file", "", "", false},
		{"new process uses flag after failure", AgyPresenceAbsent, old, newStart, 11, 22, first, "observed:file", second, "ikesu:antigravity_cli", true},
		{"same second new group uses flag", AgyPresenceAbsent, old, old, 11, 22, first, "observed:file", second, "ikesu:antigravity_cli", true},
		{"different start unknown old group uses flag", AgyPresenceAbsent, old, newStart, 0, 22, first, "observed:file", second, "ikesu:antigravity_cli", true},
		{"same start unknown old group remains uncertain", AgyPresenceAbsent, old, old, 0, 22, first, "observed:file", "", "", false},
		{"same process keeps file", AgyPresenceAbsent, old, old, 22, 22, first, "observed:file", "", "", false},
		{"handoff unknown keeps file", AgyPresenceAbsent, time.Time{}, newStart, 0, 22, first, "restore", "", "", false},
		{"multiple refuses flag", AgyPresenceAmbiguous, old, newStart, 11, 22, first, "observed:file", "", "observed:agy-ambiguous", true},
		{"ambiguous same process never falls back", AgyPresenceAbsent, newStart, newStart, 22, 22, "", "observed:agy-ambiguous", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, source, update := SelectAgySession(tc.state, "", job, tc.previousStart, tc.start, tc.previousGroup, tc.group, "agy", tc.previousID, tc.source)
			if id != tc.wantID || source != tc.wantSource || update != tc.update {
				t.Fatalf("got %q %q %v", id, source, update)
			}
		})
	}
	noFlag := platform.ForegroundJob{ProcessGroupID: 22, Processes: []platform.ForegroundProcess{{PID: 22, Name: "agy", Argv: []string{"agy"}}}}
	if id, source, update := SelectAgySession(AgyPresenceAbsent, "", noFlag, old, newStart, 11, 22, "agy", first, "observed:file"); !update || id != "" || source != "observed:agy-unresolved" {
		t.Fatalf("new process without flag: %q %q %v", id, source, update)
	}
	badFlag := platform.ForegroundJob{ProcessGroupID: 22, Processes: []platform.ForegroundProcess{{PID: 22, Name: "agy", Argv: []string{"agy", "--conversation", first, "--conversation", second}}}}
	if id, source, update := SelectAgySession(AgyPresenceAbsent, "", badFlag, old, newStart, 11, 22, "agy", first, "observed:file"); !update || id != "" || source != "observed:agy-unresolved" {
		t.Fatalf("conflicting argv: %q %q %v", id, source, update)
	}
}

func TestAgyScanRetryIsBoundedPerGeneration(t *testing.T) {
	var retry AgyScanRetry
	if !retry.MayScan() || !retry.Fail() || !retry.MayScan() {
		t.Fatal("first failure must permit retry")
	}
	if retry.Fail() || retry.MayScan() {
		t.Fatal("second failure must stop retries")
	}
	var next AgyScanRetry
	if !next.MayScan() {
		t.Fatal("new generation must retry")
	}
	next.Complete()
	if next.MayScan() {
		t.Fatal("successful generation must not be rescanned")
	}
}
