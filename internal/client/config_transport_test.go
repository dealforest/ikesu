package client

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestClientJSONPreservesEffectiveConfigVersion(t *testing.T) {
	teststate.Lock(t)
	for _, tc := range []struct {
		name, body string
		exists     bool
		version    int
		wantUI     bool
	}{
		{"unversioned file", "[ui]\nsidebar_width = 30\n", true, 1, true},
		{"missing file", "", false, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			teststate.Setenv(t, config.EnvVar, path)
			if tc.exists {
				if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConfigVersion != nil || cfg.EffectiveConfigVersion() != tc.version ||
				cfg.KoiEnabled() != tc.wantUI || cfg.SoundEnabled() != tc.wantUI {
				t.Fatal("unexpected defaults prior to encoding")
			}
			if !tc.exists && (cfg.Guard.Active() || cfg.Guard.EffectiveMode() != "record") {
				t.Fatal("file absence did not synchronize guard defaults")
			}
			peer := endpoint.Current()
			for _, kind := range []string{"hello", "view_config", "config_result"} {
				var wire bytes.Buffer
				w := &writer{conn: &wire, available: peer.Generation, methods: peer.Methods}
				if err := w.send(msg{Type: kind, Config: &cfg}); err != nil {
					t.Fatalf("%s: %v", kind, err)
				}
				var received msg
				if err := json.Unmarshal(wire.Bytes(), &received); err != nil {
					t.Fatal(err)
				}
				if got := received.Config; got == nil || got.ConfigVersion == nil ||
					got.EffectiveConfigVersion() != tc.version || got.KoiEnabled() != tc.wantUI || got.SoundEnabled() != tc.wantUI {
					t.Fatalf("%s changed effective defaults: %+v", kind, got)
				}
			}
			if cfg.ConfigVersion != nil {
				t.Fatal("wire encoding mutated the local config")
			}
		})
	}
}
