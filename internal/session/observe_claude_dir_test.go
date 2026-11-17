package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cwd のプロジェクトのフォルダがあれば、そこだけを見る。ほかのフォルダの同じ cwd の会話は数えない。
// フォルダが無ければ、今までどおり全体から探す。
func TestObserveClaudeRootUsesProjectFolder(t *testing.T) {
	start := time.Now()
	write := func(root, dir, id, cwd string) {
		t.Helper()
		d := filepath.Join(root, "projects", dir)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(map[string]string{"sessionId": id, "cwd": cwd, "timestamp": start.Format(time.RFC3339Nano)})
		if err := os.WriteFile(filepath.Join(d, id+".jsonl"), append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	cwd := "/Users/me/src/github.com/me/app"
	if got := claudeProjectDir(cwd); got != "-Users-me-src-github-com-me-app" {
		t.Fatalf("dir = %q", got)
	}
	write(root, claudeProjectDir(cwd), "current", cwd)
	write(root, "elsewhere", "stray", cwd)
	if id, ok := observeClaudeRoot(root, cwd, start); !ok || id != "current" {
		t.Fatalf("project folder: %q %v", id, ok)
	}
	other := t.TempDir()
	write(other, "renamed", "only", cwd)
	if id, ok := observeClaudeRoot(other, cwd, start); !ok || id != "only" {
		t.Fatalf("fallback walk: %q %v", id, ok)
	}
}
