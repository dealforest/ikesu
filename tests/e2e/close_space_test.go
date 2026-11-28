package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 実物の画面で、リポジトリの本体のスペースを右クリック→「Close space」→ confirm と選ぶと、
// そのスペースだけが閉じ、同じリポジトリのほかのスペース（worktree のスペース）は開いたまま残る。
// 2026-11-26、お気に入りの mangene2 を1つ閉じたら、同じリポジトリのスペース4つが全部閉じた不具合の再発防止。
func TestRightClickCloseSpaceClosesOnlyThatSpace(t *testing.T) {
	t.Parallel()
	s := start(t)
	s.waitFor("boot", 10*time.Second, func(tx string) bool { return strings.Contains(tx, "[menu]") })

	repo := filepath.Join(t.TempDir(), "closerepo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "init"}} {
		if b, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}
	// Creating worktrees opens the main checkout's space (closerepo) and
	// one space per worktree.
	for _, branch := range []string{"childone", "childtwo"} {
		if out, err := s.cli("worktree", "create", "--cwd", repo, "--branch", branch, "--label", branch, "--no-focus"); err != nil {
			t.Fatal(out, err)
		}
	}
	labels := func() []string {
		out, err := s.cli("workspace", "list")
		if err != nil {
			t.Fatal(out, err)
		}
		var r struct {
			Result struct {
				Workspaces []struct {
					Label string `json:"label"`
				} `json:"workspaces"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(out, err)
		}
		var l []string
		for _, w := range r.Result.Workspaces {
			l = append(l, w.Label)
		}
		return l
	}
	has := func(l []string, want string) bool {
		for _, v := range l {
			if v == want {
				return true
			}
		}
		return false
	}
	before := labels()
	for _, want := range []string{"closerepo", "childone", "childtwo"} {
		if !has(before, want) {
			t.Fatalf("space %s was not opened: %v", want, before)
		}
	}
	s.waitFor("the repository's spaces in the sidebar", 5*time.Second, func(tx string) bool {
		return strings.Contains(tx, "closerepo") && strings.Contains(tx, "childone") && strings.Contains(tx, "childtwo")
	})

	x, y := s.find("closerepo")
	s.rightClick(x, y)
	s.waitFor("space menu", 5*time.Second, func(tx string) bool { return strings.Contains(tx, "Close space") })
	cx, cy := s.find("Close space")
	s.click(cx, cy)
	s.waitFor("close confirmation for one space", 5*time.Second, func(tx string) bool {
		return strings.Contains(tx, "Close workspace?") && !strings.Contains(tx, "worktree group")
	})
	fx, fy := s.find("confirm")
	s.click(fx, fy)

	deadline := time.Now().Add(5 * time.Second)
	for has(labels(), "closerepo") {
		if time.Now().After(deadline) {
			t.Fatal("closerepo was not closed:", labels())
		}
		time.Sleep(50 * time.Millisecond)
	}
	after := labels()
	if !has(after, "childone") || !has(after, "childtwo") || len(after) != len(before)-1 {
		t.Fatalf("closing closerepo closed other spaces: before %v, after %v", before, after)
	}
}
