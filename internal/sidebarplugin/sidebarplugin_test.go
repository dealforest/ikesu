package sidebarplugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseName(t *testing.T) {
	for file, want := range map[string]time.Duration{"tenki.1h.sh": time.Hour, "fx.10m.py": 10 * time.Minute, "fast.1s.sh": MinInterval, "daily.2d": 48 * time.Hour} {
		if _, got, ok := ParseName(file); !ok || got != want {
			t.Errorf("%s = %v %v", file, got, ok)
		}
	}
	for _, file := range []string{"tenki.sh", ".hidden.1h.sh", "x.0m.sh", "README.md"} {
		if _, _, ok := ParseName(file); ok {
			t.Errorf("%s parsed", file)
		}
	}
	if name, _, _ := ParseName("tenki.1h.sh"); name != "tenki" {
		t.Errorf("name = %q", name)
	}
}

func TestLines(t *testing.T) {
	got := Lines([]byte("\n\x1b[31m☀ 晴れ\x1b[0m 18℃ | color=red\n\x1b]0;title\x07second\x01 line\n\n---\nmenu\n"))
	want := []string{"\x1b[31m☀ 晴れ\x1b[0m 18℃", "second line"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lines = %q", got)
	}
	if got := Lines([]byte("---\nmenu\n")); len(got) != 0 {
		t.Fatalf("menu only = %q", got)
	}
}

// 実行できるスクリプトだけを動かし、1行目を出す。時間切れはエラーにして前の表示を残す。
func TestRunnerRunsExecutableScripts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("tenki.1h.sh", "#!/bin/sh\necho 'sunny 18C'\necho \"cols=$IKESU_SIDEBAR_COLUMNS\"\n", 0o755)
	write("off.1h.sh", "#!/bin/sh\necho never\n", 0o644)
	write("fail.1h.sh", "#!/bin/sh\nexit 3\n", 0o755)
	changed := make(chan struct{}, 16)
	r := &Runner{Dir: dir, OnChange: func() { changed <- struct{}{} }, Columns: func() int { return 24 }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	deadline := time.After(10 * time.Second)
	for {
		items := r.Items()
		if len(items) == 2 && items[0].Ran && items[1].Ran {
			if items[0].File != "fail.1h.sh" || items[0].Err == "" || len(items[1].Lines) != 2 || items[1].Lines[0] != "sunny 18C" || items[1].Lines[1] != "cols=24" || items[1].Err != "" {
				t.Fatalf("items = %+v", items)
			}
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("plugins did not run: %+v", r.Items())
		}
	}
}
