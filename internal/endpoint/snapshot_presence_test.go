package endpoint

import (
	"reflect"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/model"
	"github.com/kazuph/ikesu/internal/render"
)

func TestSnapshotConfigVersionAndOptInPresence(t *testing.T) {
	t.Parallel()
	v0, v1, v2 := 0, 1, 2
	off, on := false, true
	var enc SnapshotEncoder
	var dec SnapshotDecoder
	in := render.Input{State: model.New("/tmp", 80, 24)}
	for _, version := range []*int{nil, &v0, &v1, &v2} {
		for _, plugins := range []*bool{nil, &off, &on} {
			for _, teams := range []*bool{nil, &off, &on} {
				for _, loaded := range []bool{true, false} {
					var c config.Config
					c.ConfigVersion = version
					c.UI.SidebarPlugins.Enabled = plugins
					c.Integrations.ClaudeTeams = teams
					c.SetLoadedFromFile(loaded)
					in.SidebarConfig = c
					data, err := enc.Encode(in)
					if err != nil {
						t.Fatal(err)
					}
					got, err := dec.Decode(data)
					if err != nil {
						t.Fatal(err)
					}
					back := got.SidebarConfig
					if !reflect.DeepEqual(c, back) || c.EffectiveConfigVersion() != back.EffectiveConfigVersion() ||
						c.SidebarPluginsEnabled() != back.SidebarPluginsEnabled() || c.ClaudeTeamsEnabled() != back.ClaudeTeamsEnabled() {
						t.Fatalf("config changed during snapshot round trip: %+v → %+v", c, back)
					}
				}
			}
		}
	}
}

func TestLegacySnapshotUsesV1ConfigDefaults(t *testing.T) {
	var in render.Input
	if err := (snapshotPresence{}).restore(&in); err != nil {
		t.Fatal(err)
	}
	if got := in.SidebarConfig; !got.LoadedFromFile() || got.EffectiveConfigVersion() != config.ConfigVersionV1 || !got.KoiEnabled() {
		t.Fatalf("legacy snapshot resolved to config version %d, koi=%t", got.EffectiveConfigVersion(), got.KoiEnabled())
	}
}
