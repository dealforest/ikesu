package guard_test

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/guard"
)

func TestGuardHandoffPreservesDisabledPolicies(t *testing.T) {
	for _, tc := range []struct {
		name, toml string
		active     bool
	}{
		{"v2 default", "config_version = 2", false},
		{"explicit false", "[guard]\nenabled = false", false},
		{"legacy default", "", true},
		{"explicit true", "config_version = 2\n[guard]\nenabled = true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Decode([]byte(tc.toml))
			if err != nil {
				t.Fatal(err)
			}
			assets := guard.NewAssets(t.TempDir(), "p1", "", cfg.GuardConfig())
			var wire bytes.Buffer
			if err := gob.NewEncoder(&wire).Encode(assets.Transfer()); err != nil {
				t.Fatal(err)
			}
			var transfer guard.Transfer
			if err := gob.NewDecoder(&wire).Decode(&transfer); err != nil {
				t.Fatal(err)
			}
			got := guard.Import(transfer).Config
			if got.Active() != tc.active || got.EffectiveMode() != cfg.GuardConfig().EffectiveMode() {
				t.Fatalf("imported active=%t mode=%q", got.Active(), got.EffectiveMode())
			}
		})
	}
}
