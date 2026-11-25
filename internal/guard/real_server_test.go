package guard

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/childenv"
)

// Building cmd/ikesu takes seconds per build, so the package builds it once and
// every test that runs the real CLI gets its own copy of that one binary.
var sharedGuardBinary struct {
	once      sync.Once
	dir, path string
	err       error
}

// copyGuardBinary copies the package's one build of cmd/ikesu to dst. It copies
// rather than hard-links: a new link changes the shared file's ctime, which is
// part of the identity the guard trusts, so a running server would see its
// executable as replaced. dst stays inside the caller's own directory, so tests keep their
// existing directory layout.
func copyGuardBinary(dst string) error {
	sharedGuardBinary.once.Do(func() {
		dir, err := os.MkdirTemp("/tmp", "ikesu-guard-bin-")
		if err != nil {
			sharedGuardBinary.err = err
			return
		}
		sharedGuardBinary.dir = dir
		path := filepath.Join(dir, "ikesu")
		if out, err := exec.Command("go", "build", "-o", path, "../../cmd/ikesu").CombinedOutput(); err != nil {
			sharedGuardBinary.err = fmt.Errorf("build real guard CLI: %w: %s", err, out)
			return
		}
		sharedGuardBinary.path = path
	})
	if sharedGuardBinary.err != nil {
		return sharedGuardBinary.err
	}
	src, err := os.Open(sharedGuardBinary.path)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func guardBinary(t *testing.T, dst string) {
	t.Helper()
	if err := copyGuardBinary(dst); err != nil {
		t.Fatal(err)
	}
}

var guardFixture struct {
	once         sync.Once
	root, socket string
	server       *exec.Cmd
	err          error
}

// These runtime tests execute the actual guard CLI. Its identity checks use a
// real isolated server, shared across tests to avoid starting one per case.
func realGuardFixture(t *testing.T) string {
	t.Helper()
	guardFixture.once.Do(func() {
		root, err := os.MkdirTemp("/tmp", "ikesu-guard-unit-")
		if err != nil {
			guardFixture.err = err
			return
		}
		guardFixture.root = root
		binary := filepath.Join(root, "ikesu")
		if err := copyGuardBinary(binary); err != nil {
			guardFixture.err = err
			return
		}
		cfg := filepath.Join(root, "config.toml")
		if err := os.WriteFile(cfg, []byte("onboarding=false\n[session]\nresume_agents_on_restore=false\n"), 0600); err != nil {
			guardFixture.err = err
			return
		}
		cmd := exec.Command(binary, "--session", "fixture", "server")
		cmd.Env = append(childenv.Scrub(os.Environ()), "HOME="+root, "SHELL=/bin/sh", "IKESU_STATE_DIR="+root, "IKESU_CONFIG="+cfg)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			guardFixture.err = err
			return
		}
		guardFixture.server = cmd
		guardFixture.socket = api.APISocketPath(filepath.Join(root, "fixture"))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("unix", guardFixture.socket, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		guardFixture.err = fmt.Errorf("isolated server did not start")
	})
	if guardFixture.err != nil {
		t.Fatal(guardFixture.err)
	}
	return guardFixture.socket
}

func TestMain(m *testing.M) {
	probeTimeout, ghProbeTimeout = 30*time.Second, 30*time.Second
	code := m.Run()
	if guardFixture.server != nil {
		if conn, err := net.DialTimeout("unix", guardFixture.socket, time.Second); err == nil {
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			_ = api.WriteRequest(conn, api.Request{ID: "cleanup", Method: api.MethodServerStop, Params: api.MarshalParams(nil)})
			_, _ = io.Copy(io.Discard, conn)
			_ = conn.Close()
		}
		if err := guardFixture.server.Wait(); err != nil {
			fmt.Fprintln(os.Stderr, "isolated guard server:", err)
			code = 1
		}
	}
	if sharedGuardBinary.dir != "" {
		if err := os.RemoveAll(sharedGuardBinary.dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	if guardFixture.root != "" {
		if err := os.RemoveAll(guardFixture.root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}
