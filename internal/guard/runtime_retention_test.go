package guard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureGenerationPrunesExpiredGenerations(t *testing.T) {
	t.Parallel()
	a := NewAssets(t.TempDir(), "p_1", "/usr/bin/true", Config{})
	shell, err := a.privateShell(true)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.close()
	unlock, err := shell.lock(true)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	previous, err := a.ensureGeneration(shell, true)
	if err != nil {
		t.Fatal(err)
	}
	build := func() string {
		t.Helper()
		name, err := a.buildGeneration(shell)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Join(shell.path, name)
	}
	old, recent, blocked := build(), build(), build()
	// A directory at a payload name causes Unlinkat to fail on every host.
	payload := filepath.Join(blocked, "manifest.json")
	if err := os.Remove(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(payload, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for path, age := range map[string]time.Duration{
		previous: 2 * time.Hour, old: 61 * time.Minute,
		recent: 59 * time.Minute, blocked: 61 * time.Minute,
	} {
		when := now.Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	neighbor := filepath.Join(shell.path, "unrelated")
	if err := os.Mkdir(neighbor, 0700); err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-time.Hour)
	if err := os.Chtimes(neighbor, expired, expired); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(shell.path, "generation-linked")
	if err := os.Symlink(neighbor, link); err != nil {
		t.Fatal(err)
	}
	// A policy update makes the previously published manifest stale.
	a.Config.Mode = "record"
	current, err := a.ensureGeneration(shell, true)
	if err != nil {
		t.Fatal(err)
	}
	if current == previous {
		t.Fatal("policy update did not create a new generation")
	}
	if name, err := shell.readlink("current"); err != nil || name != filepath.Base(current) {
		t.Fatalf("current points to %q, error %v", name, err)
	}
	for _, path := range []string{old} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("expired generation remains: %s (%v)", filepath.Base(path), err)
		}
	}
	for _, path := range []string{current, previous, recent, payload, neighbor, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("expected %s to remain: %v", filepath.Base(path), err)
		}
	}
	retired, err := os.Stat(previous)
	if err != nil || retired.ModTime().Before(now) {
		t.Fatalf("previous generation did not get a retirement time: %v, %v", retired, err)
	}
	// Independently verify the exemption for current even with an old mtime.
	if err := os.Chtimes(current, expired, expired); err != nil {
		t.Fatal(err)
	}
	pruneGenerations(shell, filepath.Base(current), now.Add(-time.Hour))
	if _, err := os.Stat(filepath.Join(current, "manifest.json")); err != nil {
		t.Fatalf("current generation was pruned: %v", err)
	}
}
