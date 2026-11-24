package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/guard"
	"github.com/kazuph/ikesu/internal/runtime"
	"github.com/kazuph/ikesu/internal/teststate"
)

type gatedPaneGuard struct {
	runtime.PaneGuard
	entered chan struct{}
	release chan struct{}
}

func (g *gatedPaneGuard) Reconfigure(c guard.Config) error {
	if c.AskTimeoutSeconds != nil && *c.AskTimeoutSeconds == 120 {
		close(g.entered)
		<-g.release
	}
	return g.PaneGuard.Reconfigure(c)
}

func TestReloadConfigWaitsForPaneGuardOutsideAppLoop(t *testing.T) {
	teststate.Lock(t)
	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	teststate.Setenv(t, config.EnvVar, path)
	teststate.Setenv(t, "SHELL", "/bin/sh")
	writeConfig := func(timeout int) {
		t.Helper()
		if err := os.WriteFile(path, []byte(fmt.Sprintf("onboarding = false\n[guard]\nmode = \"record\"\nask_timeout_seconds = %d\n", timeout)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(60)
	s := newTestServer(t, "guard-reload")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg = cfg
	s.configPath = path
	s.app.SetConfigChanged(s.screenPromptConfig)
	s.app.SetGuardRoot(root)
	s.app.SetSidebarConfig(cfg, true)
	gate := &gatedPaneGuard{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate.release) }) }
	s.sup.NewPaneGuard = func(root, pane, executable string, c guard.Config) runtime.PaneGuard {
		assets := guard.NewAssets(root, pane, executable, c)
		if pane == "p_1" {
			gate.PaneGuard = assets
			return gate
		}
		return assets
	}
	socket := filepath.Join(root, "guard.sock")
	pane, err := s.sup.SpawnWith(1, root, 80, 24, []string{"IKESU_SOCKET_PATH=" + socket}, []string{"/bin/sleep", "60"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.app.Run(); close(done) }()
	t.Cleanup(func() {
		release()
		s.app.Stop()
		<-done
		s.sup.KillAll()
		<-pane.Exited()
	})
	request := func(method string, params any) api.Response {
		return s.dispatch(&api.Request{Method: method, Params: api.MarshalParams(params)})
	}
	if resp := request(api.MethodPaneSplit, api.PaneSplitParams{Direction: "right"}); resp.Err != nil {
		t.Fatal(resp.Err)
	}
	if resp := request(api.MethodPaneReportAgent, api.PaneReportAgentParams{PaneID: "p1", Agent: "codex", State: "working", Source: "test"}); resp.Err != nil {
		t.Fatal(resp.Err)
	}
	readMarker := func(timeout int) guard.Marker {
		t.Helper()
		m, active, err := guard.ReadMarker(guard.Input{Env: map[string]string{
			"IKESU_GUARD_MARKER": pane.GuardMarkerPath(), "IKESU_PANE_ID": "p1", "IKESU_SOCKET_PATH": socket,
		}})
		if err != nil || !active || m.Agent != "codex" || m.Mode != "record" || m.Policy == nil || m.Policy.AskTimeoutSeconds == nil || *m.Policy.AskTimeoutSeconds != timeout {
			t.Fatalf("marker active=%v, got %+v, err=%v, want timeout %ds", active, m, err, timeout)
		}
		return m
	}
	readMarker(60)
	writeConfig(120)
	reloaded := make(chan api.Response, 1)
	go func() { reloaded <- request(api.MethodServerReloadConfig, api.EmptyParams{}) }()
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not reach the guard worker")
	}
	other := make(chan api.Response, 1)
	go func() { other <- request(api.MethodPaneGet, api.PaneTarget{PaneID: "p2"}) }()
	select {
	case resp := <-other:
		if resp.Err != nil || len(resp.Result) == 0 {
			t.Fatalf("second pane response: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second pane RPC blocked behind guard I/O")
	}
	readMarker(60)
	select {
	case resp := <-reloaded:
		t.Fatalf("reload acknowledged the previous marker: %+v", resp)
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case resp := <-reloaded:
		var result api.ConfigReloadResult
		if resp.Err != nil || json.Unmarshal(resp.Result, &result) != nil || result.Status != "applied" {
			t.Fatalf("reload response: %+v, result=%+v", resp, result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not finish after the guard worker completed")
	}
	marker := readMarker(120)
	pid, start := pane.ShellIdentity()
	resp := s.dispatch(&api.Request{Method: api.MethodGuardIdentity, PeerPID: pid, Params: api.MarshalParams(api.GuardIdentityParams{PaneID: "p1", PID: pid, StartTime: start})})
	var identity api.GuardIdentityResult
	if resp.Err != nil || json.Unmarshal(resp.Result, &identity) != nil || !identity.Match || !bytes.Equal(identity.Policy, api.MarshalParams(*marker.Policy)) {
		t.Fatalf("guard.identity did not match the marker policy: %+v, identity=%+v, marker=%+v", resp, identity, marker)
	}
}
