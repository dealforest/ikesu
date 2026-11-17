// Package platform collects the OS-specific bits ikesu needs: foreground
// process of a PTY, process names, argv, cwd, and input flushing.
// Everything else in the codebase stays platform-neutral.
package platform

import (
	"github.com/kazuph/ikesu/internal/childenv"
	"strconv"
	"strings"
	"time"
)

// ForegroundPID returns the process-group leader pid of the process in the
// foreground of the terminal behind fd (a PTY master), or 0 on failure.
func ForegroundPID(fd int) int { return foregroundPID(fd) }

// ProcName returns the short process name (comm) of pid.
func ProcName(pid int) string { return procName(pid) }

// ProcArgs returns the argv of pid, best effort.
func ProcArgs(pid int) []string { return procArgs(pid) }

// ProcCwd returns the current working directory of pid, best effort.
func ProcCwd(pid int) string { return procCwd(pid) }

// FlushInput discards pending terminal input on fd.
func FlushInput(fd int) error { return flushInput(fd) }

// ForegroundProcess describes one member of a foreground process group.
type ForegroundProcess struct {
	PID     int
	Name    string   // comm / short executable name
	Argv    []string // argv, best effort; Argv[0] is argv0
	Cmdline string   // argv joined with spaces, best effort
}

// ForegroundJob is the set of processes in the terminal's foreground
// process group.
type ForegroundJob struct {
	ProcessGroupID int
	Sudo           bool
	Processes      []ForegroundProcess
}

// ForegroundJobOf returns the foreground process group of the terminal
// behind fd (a PTY master), or an empty job on failure.
func ForegroundJobOf(fd int) ForegroundJob { return foregroundJob(fd) }

// PPID returns the parent pid of pid, or 0 when unknown. Implemented via
// `ps` so it works on both darwin and linux without extra plumbing; this is
// called a handful of times per pane.current request, not in hot loops.
func PPID(pid int) int {
	out, err := childenv.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// ProcStart returns the start time of pid, best effort, via `ps etimes`.
// Used by the session-file observer to correlate a freshly created agent
// session file with the process that started it.
func ProcStart(pid int) (time.Time, bool) {
	// macOS は sysctl で読む。ps を起動すると、ペインの観察のたびにプロセスを作り、サーバーの CPU を食っていた
	// （2026-11-17 の CPU プロファイルで ps の起動と環境の掃除が約2割）。
	return procStartTime(pid)
}
