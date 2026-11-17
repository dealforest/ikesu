package cli

import (
	"path/filepath"
	"testing"

	"github.com/kazuph/ikesu/internal/api"
)

// guard の記録を読むコマンドも、サーバーと同じセッションの場所を見る（Issue #67）。
func TestGuardStateRootFollowsSession(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		o    Options
		want string
	}{
		{Options{Getenv: env(nil)}, api.StateDir()},
		{Options{Session: "default", Getenv: env(nil)}, api.StateDir()},
		{Options{Session: "proof", Getenv: env(nil)}, api.SessionDir("proof")},
		{Options{Getenv: env(map[string]string{"IKESU_SOCKET_PATH": api.APISocketPath(api.SessionDir("proof"))})}, api.SessionDir("proof")},
		{Options{Getenv: env(map[string]string{"IKESU_SOCKET_PATH": api.APISocketPath(api.SessionDir("default"))})}, api.StateDir()},
		{Options{Getenv: env(map[string]string{"IKESU_SOCKET_PATH": filepath.Join(t.TempDir(), "x", "ikesu.sock")})}, api.StateDir()},
	}
	for i, c := range cases {
		if got := (&runner{o: c.o}).guardStateRoot(); got != c.want {
			t.Errorf("case %d: %q, want %q", i, got, c.want)
		}
	}
}
