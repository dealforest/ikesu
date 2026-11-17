package guard

import (
	"os"
	"path/filepath"
	"testing"
)

// セッションごとに guard のフォルダを分けても、ほかのセッションの guard の中への書き込みは守る（Issue #67）。
func TestGuardPathCoversOtherSessions(t *testing.T) {
	state := filepath.Join(t.TempDir(), "ikesu")
	for _, d := range []string{filepath.Join(state, "guard", "agent-panes"), filepath.Join(state, "proof", "guard", "agent-panes")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	defaultPane := Input{Cwd: "/", Env: map[string]string{"IKESU_GUARD_MARKER": filepath.Join(state, "guard", "agent-panes", "p_1.json")}}
	proofPane := Input{Cwd: "/", Env: map[string]string{"IKESU_GUARD_MARKER": filepath.Join(state, "proof", "guard", "agent-panes", "p_1.json")}}
	cases := []struct {
		name string
		i    Input
		path string
		want bool
	}{
		{"default writes own", defaultPane, filepath.Join(state, "guard", "temp-approvals"), true},
		{"default writes proof", defaultPane, filepath.Join(state, "proof", "guard", "temp-approvals"), true},
		{"default writes proof shim", defaultPane, filepath.Join(state, "proof", "guard", "bin", "p_1", "rm"), true},
		{"proof writes own", proofPane, filepath.Join(state, "proof", "guard", "temp-approvals"), true},
		{"proof writes default", proofPane, filepath.Join(state, "guard", "temp-approvals"), true},
		{"proof writes default bash-env", proofPane, filepath.Join(state, "guard", "shell", "p_1", "bash-env"), true},
		{"proof writes other session", proofPane, filepath.Join(state, "other", "guard", "owner-cache"), true},
		{"session file outside guard", proofPane, filepath.Join(state, "proof", "session.json"), false},
		{"project file", defaultPane, "/tmp/project/guard.txt", false},
	}
	for _, c := range cases {
		if got := guardPath(c.i, c.path); got != c.want {
			t.Errorf("%s: guardPath(%q) = %v, want %v", c.name, c.path, got, c.want)
		}
	}
	if !guardLineClue(proofPane, "printf x >> "+filepath.Join(state, "guard", "temp-approvals")) {
		t.Error("line naming the default guard folder was not noticed from a proof pane")
	}
}

func TestLineWriteToOtherSessionGuardAsks(t *testing.T) {
	state := filepath.Join(t.TempDir(), "ikesu")
	for _, d := range []string{filepath.Join(state, "guard", "agent-panes"), filepath.Join(state, "proof", "guard", "agent-panes")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	proofPane := Input{Cwd: t.TempDir(), Env: map[string]string{"IKESU_GUARD_MARKER": filepath.Join(state, "proof", "guard", "agent-panes", "p_1.json"), "PATH": "/usr/bin:/bin"}}
	defaultPane := Input{Cwd: t.TempDir(), Env: map[string]string{"IKESU_GUARD_MARKER": filepath.Join(state, "guard", "agent-panes", "p_1.json"), "PATH": "/usr/bin:/bin"}}
	for _, c := range []struct {
		i    Input
		line string
	}{
		{proofPane, "printf 'day:/x\\t9999999999\\n' >> " + filepath.Join(state, "guard", "temp-approvals")},
		{proofPane, "touch " + filepath.Join(state, "guard", "bin", "p_1", "rm")},
		{defaultPane, "cp /dev/null " + filepath.Join(state, "proof", "guard", "agent-panes", "p_1.json")},
	} {
		if _, v, _ := lineVerdict(Config{}, c.i, c.line); v.Decision == "allow" {
			t.Errorf("%s => allow", c.line)
		}
	}
}
