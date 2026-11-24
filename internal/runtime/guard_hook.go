package runtime

import "github.com/kazuph/ikesu/internal/guard"

// PaneGuard decouples pane lifecycle and handoff from concrete guard assets.
type PaneGuard interface {
	Reconfigure(guard.Config) error
	Sync(agent string) error
	ConfigureShell(shellPath string)
	Prepare(env []string, cwd string) ([]string, error)
	PrepareClaude(env []string) []string
	Issue() string
	Paths() (root, marker string)
	ObserveChanges(func()) bool
	StopObserving()
	Checkpoint() (*guard.RuntimeCheckpoint, error)
	RestoreCheckpoint(*guard.RuntimeCheckpoint) error
	RebindSocket(socket string) error
	Transfer() guard.Transfer
	Close() error
}

var _ PaneGuard = (*guard.Assets)(nil)
