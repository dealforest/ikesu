package session

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/detect"
	"github.com/kazuph/ikesu/internal/platform"
	"golang.org/x/sys/unix"
)

var agyPresenceName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.lock$`)

type AgyPresenceState uint8

const (
	AgyPresenceUnknown AgyPresenceState = iota
	AgyPresenceAbsent
	AgyPresenceUnique
	AgyPresenceAmbiguous
)

func observeAgyPresence(home string, job platform.ForegroundJob) (string, AgyPresenceState) {
	if !filepath.IsAbs(home) || job.ProcessGroupID <= 0 {
		return "", AgyPresenceUnknown
	}
	root := filepath.Join(home, ".gemini", "antigravity-cli", "presence")
	ids := map[string]bool{}
	matched := false
	for _, p := range job.Processes {
		if detect.Identify(p.Name, firstArg(p.Argv)) != "agy" {
			continue
		}
		matched = true
		before, ok := platform.ProcStart(p.PID)
		group, err := unix.Getpgid(p.PID)
		if !ok || err != nil || group != job.ProcessGroupID || detect.Identify(platform.ProcName(p.PID), firstArg(platform.ProcArgs(p.PID))) != "agy" {
			return "", AgyPresenceUnknown
		}
		paths, readErr := platform.OpenRegularFiles(p.PID)
		if readErr != nil {
			return "", AgyPresenceUnknown
		}
		after, alive := platform.ProcStart(p.PID)
		group, err = unix.Getpgid(p.PID)
		if !alive || !before.Equal(after) || err != nil || group != job.ProcessGroupID || detect.Identify(platform.ProcName(p.PID), firstArg(platform.ProcArgs(p.PID))) != "agy" {
			return "", AgyPresenceUnknown
		}
		for _, path := range paths {
			name := filepath.Base(path)
			if filepath.Dir(path) == root && agyPresenceName.MatchString(name) {
				ids[strings.TrimSuffix(name, ".lock")] = true
			}
		}
	}
	if !matched {
		return "", AgyPresenceUnknown
	}
	if len(ids) > 1 {
		return "", AgyPresenceAmbiguous
	}
	if len(ids) == 0 {
		return "", AgyPresenceAbsent
	}
	for id := range ids {
		return id, AgyPresenceUnique
	}
	return "", AgyPresenceUnknown
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// ObserveAgyPresence distinguishes a verified empty FD set from an unreadable
// or ambiguous one; only the former permits a launch-argument fallback.
func ObserveAgyPresence(job platform.ForegroundJob, env []string) (string, AgyPresenceState) {
	home := ""
	for _, item := range env {
		if strings.HasPrefix(item, "HOME=") {
			home = strings.TrimPrefix(item, "HOME=")
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return observeAgyPresence(home, job)
}

// SelectAgySession reconciles live FD evidence, an exact launch flag and a
// previous association. An unknown previous start is common after handoff;
// retain its confirmed ID until a live FD identifies the current session.
func SelectAgySession(state AgyPresenceState, id string, job platform.ForegroundJob, previousStart, start time.Time, previousGroup, group int, previousAgent, previousID, previousSource string) (string, string, bool) {
	if state == AgyPresenceUnique {
		return id, "observed:file", true
	}
	if state == AgyPresenceUnknown {
		return "", "", false
	}
	if state == AgyPresenceAmbiguous {
		if previousAgent == "agy" && previousID == "" && previousSource == "observed:agy-ambiguous" {
			return "", "", false
		}
		return "", "observed:agy-ambiguous", true
	}
	knownSame := previousStart.IsZero() || (previousStart.Equal(start) && (previousGroup == 0 || previousGroup == group))
	if previousAgent == "agy" && knownSame && (previousID != "" || previousSource == "observed:agy-ambiguous") {
		return "", "", false
	}
	var launchID string
	for _, p := range job.Processes {
		if detect.Identify(p.Name, firstArg(p.Argv)) != "agy" {
			continue
		}
		value, _, ok := ObserveNativeResume(p)
		if !ok {
			return "", "observed:agy-unresolved", previousAgent == "agy" && !knownSame
		}
		if value == "" {
			continue
		}
		if launchID != "" && launchID != value {
			return "", "observed:agy-unresolved", previousAgent == "agy" && !knownSame
		}
		launchID = value
	}
	if launchID != "" {
		return launchID, "ikesu:antigravity_cli", true
	}
	if previousAgent == "agy" && !knownSame && previousID != "" {
		return "", "observed:agy-unresolved", true
	}
	return "", "", false
}

// AgyScanRetry permits one retry of a failed observation for a given pane
// generation. A new generation starts with the zero value again.
type AgyScanRetry struct {
	failures uint8
	done     bool
}

func (r AgyScanRetry) MayScan() bool { return !r.done && r.failures < 2 }
func (r *AgyScanRetry) Fail() bool {
	if r.failures < 2 {
		r.failures++
	}
	return r.MayScan()
}
func (r *AgyScanRetry) Complete() { r.done = true }
