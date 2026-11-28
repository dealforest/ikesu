package guard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
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
	unlock()
	locked = false
	prune := func(cutoff time.Time) {
		t.Helper()
		pruner, err := prepareGenerationPruner(a.Root, shell)
		if err != nil {
			t.Fatal(err)
		}
		pruner.prune(shell, cutoff)
	}
	prune(time.Now().Add(-generationRetention))
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Errorf("expired generation remains: %s (%v)", filepath.Base(old), err)
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
	prune(now.Add(-time.Hour))
	if _, err := os.Stat(filepath.Join(current, "manifest.json")); err != nil {
		t.Fatalf("current generation was pruned: %v", err)
	}
}

// A cleanup worker may already own the prune lock. Publication must be
// independent of that worker and of the number of generations it will visit.
func TestGuardPublicationDoesNotWaitForGenerationBacklog(t *testing.T) {
	t.Parallel()
	a := NewAssets(t.TempDir(), "p_1", "/usr/bin/true", Config{})
	shell, err := a.privateShell(true)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.close()
	lock, err := os.OpenFile(filepath.Join(shell.path, "generation-prune.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	publish := func() time.Duration {
		t.Helper()
		start := time.Now()
		if err := a.publishRuntime(); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	empty := publish()
	count := 1001
	if countStr := os.Getenv("IKESU_TEST_GENERATION_BACKLOG"); countStr != "" {
		var err error
		count, err = strconv.Atoi(countStr)
		if err != nil || count <= 0 {
			t.Fatalf("invalid generation backlog count %q", countStr)
		}
	}
	backlog := seedExpiredGenerations(t, shell.path, count)
	a.Config.Mode = "record"
	loaded := publish()
	t.Logf("publication without backlog: %s, with %d expired generations: %s", empty, count, loaded)
	if !a.runtimeIntact() {
		t.Fatal("new generation was not available while cleanup was busy")
	}
	for _, dir := range backlog {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("publication unexpectedly cleaned backlog: %v", err)
		}
	}
}

func seedExpiredGenerations(t testing.TB, shell string, count int) []string {
	t.Helper()
	when := time.Now().Add(-2 * time.Hour)
	dirs := make([]string, 0, count)
	for n := range count {
		dir := filepath.Join(shell, fmt.Sprintf("generation-backlog-%d", n))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	return dirs
}

func TestPublishGenerationRejectsCurrentReadlinkError(t *testing.T) {
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
	payload := filepath.Join(previous, "bash-env.current")
	before, err := os.ReadFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	// A regular file at current yields EINVAL from readlink, while rename
	// would still be able to replace the entry with a symlink.
	current := filepath.Join(shell.path, "current")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	original := []byte("unreadable as a symlink")
	if err := os.WriteFile(current, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ensureGeneration(shell, true); !errors.Is(err, unix.EINVAL) {
		t.Errorf("publication should return readlink EINVAL, got %v", err)
	}
	if after, err := os.ReadFile(current); err != nil || !bytes.Equal(after, original) {
		t.Errorf("publication replaced current despite the readlink failure: %v", err)
	}
	if after, err := os.ReadFile(payload); err != nil || !bytes.Equal(after, before) {
		t.Errorf("publication changed the previous startup file: %v", err)
	}
}

func TestGenerationPruningRetainsLatestRetirementsAcrossClockChanges(t *testing.T) {
	t.Parallel()
	for _, direction := range []string{"forward", "backward"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()
			a := NewAssets(t.TempDir(), "p_1", "/usr/bin/true", Config{})
			shell, err := a.privateShell(true)
			if err != nil {
				t.Fatal(err)
			}
			defer shell.close()

			var retired []string
			var current string
			for i := range 67 {
				a.Config.AskTimeoutSeconds = &i
				path, err := func() (string, error) {
					unlock, err := shell.lock(true)
					if err != nil {
						return "", err
					}
					defer unlock()
					return a.ensureGeneration(shell, true)
				}()
				if err != nil {
					t.Fatal(err)
				}
				if current != "" {
					retired = append(retired, current)
				}
				current = path
			}
			// A shell acquired this immutable startup path before publication
			// of the new current. Pruning must leave its contents readable.
			acquired := filepath.Join(retired[len(retired)-1], "bash-env.current")
			before, err := os.ReadFile(acquired)
			if err != nil {
				t.Fatal(err)
			}

			now := time.Now()
			cutoff := now.Add(2*time.Hour - generationRetention)
			if direction == "backward" {
				// The worker chose its cutoff before the clock went back.
				// Retirement times written after the jump precede that cutoff.
				cutoff = now.Add(-generationRetention)
				afterJump := now.Add(-2 * time.Hour)
				for _, path := range retired {
					if err := os.Chtimes(path, afterJump, afterJump); err != nil {
						t.Fatal(err)
					}
				}
			}
			pruner, err := prepareGenerationPruner(a.Root, shell)
			if err != nil {
				t.Fatal(err)
			}
			pruner.prune(shell, cutoff)

			if after, err := os.ReadFile(acquired); err != nil || !bytes.Equal(after, before) {
				t.Errorf("acquired startup file was lost after clock moved %s: %v", direction, err)
			}
			for i, path := range retired {
				_, err := os.Stat(path)
				if i >= len(retired)-8 {
					if err != nil {
						t.Errorf("recent retirement %d was pruned: %v", i, err)
					}
				} else if !os.IsNotExist(err) {
					t.Errorf("expired retirement %d remains: %v", i, err)
				}
			}
			if _, err := os.Stat(filepath.Join(current, "bash-env.current")); err != nil {
				t.Errorf("current startup file was pruned: %v", err)
			}
			data, err := shell.read("generation-retirements")
			var want []string
			for _, path := range retired[len(retired)-64:] {
				want = append(want, filepath.Base(path))
			}
			var got []string
			if err != nil {
				t.Error(err)
			} else if err := json.Unmarshal(data, &got); err != nil || !slices.Equal(got, want) {
				t.Errorf("expected the last 64 retirements in order, got %d entries: %v", len(got), err)
			}
		})
	}
}
