package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kazuph/ikesu/internal/endpoint"
)

func TestSelectionFileKeepsTheLastSelectionPerSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screens", "phone.json")
	t.Setenv(selectionFileEnv, path)
	f := newSelectionFile("default")
	if got := f.load(); got != nil {
		t.Fatalf("empty file loaded %+v", got)
	}
	want := &endpoint.ViewSelection{Workspace: 2, Tabs: map[int]int{2: 5}, Pane: 9}
	f.save(want)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// 同じ選択では書き直さない（毎回の描画で書かない）。
	f.save(&endpoint.ViewSelection{Workspace: 2, Tabs: map[int]int{2: 5}, Pane: 9})
	if again, _ := os.Stat(path); !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("unchanged selection was written again")
	}
	next := newSelectionFile("default")
	got := next.load()
	if got == nil || got.Workspace != 2 || got.Tabs[2] != 5 || got.Pane != 9 {
		t.Fatalf("loaded %+v", got)
	}
	// ほかのセッションの選択は使わない。
	if other := newSelectionFile("work").load(); other != nil {
		t.Fatalf("other session loaded %+v", other)
	}
}

func TestSelectionFileOffWithoutEnv(t *testing.T) {
	t.Setenv(selectionFileEnv, "")
	f := newSelectionFile("default")
	if f != nil {
		t.Fatal("selection file without env")
	}
	f.save(&endpoint.ViewSelection{Workspace: 1})
	if f.load() != nil {
		t.Fatal("nil file loaded something")
	}
}

func TestSelectionFileIgnoresBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phone.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(selectionFileEnv, path)
	if got := newSelectionFile("default").load(); got != nil {
		t.Fatalf("broken file loaded %+v", got)
	}
}
