package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/input"
	"github.com/kazuph/ikesu/internal/machine"
)

func TestMachineCatalogDisconnectReturnsToLocalAndStopsRetry(t *testing.T) {
	t.Setenv("IKESU_STATE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	peer := endpoint.Current()
	local := &endpointConnection{writer: &writer{available: peer.Generation, methods: peer.Methods}}
	m := &machineClient{session: "catalog-test", endpoints: map[string]*managedEndpoint{
		"local": {connection: local},
	}}
	catalog := machine.Catalog{Path: m.catalogPath()}
	profile, err := catalog.AddVerified("host", "Remote", "default")
	if err != nil {
		t.Fatal(err)
	}
	m.active = profile.ID
	m.pending = &pendingMachine{id: profile.ID, keys: []input.Event{{Kind: input.KindKey}}}
	remote := &managedEndpoint{profile: profile, cancel: cancel, attempt: 1, retry: retryPolicy{next: now, delay: initialRetryDelay}}
	m.endpoints[profile.ID] = remote
	if _, err := catalog.Change(profile.ID, "disable", "", nil); err != nil {
		t.Fatal(err)
	}
	m.event(endpointEvent{catalogChanged: true})
	m.event(endpointEvent{id: profile.ID, attempt: 1, err: errors.New("late SSH error")})
	m.advance(now)
	if m.active != "local" || m.pending != nil || ctx.Err() == nil || remote.connection != nil || remote.retry != (retryPolicy{}) || remote.attempt != 2 {
		t.Fatalf("disconnect left active=%s pending=%+v cancelled=%v remote=%+v", m.active, m.pending, ctx.Err(), remote)
	}
	if remote.profile.Enabled || remote.status != "disconnected" || remote.reason != "machine is disconnected" {
		t.Fatalf("unexpected disconnected state: %+v", remote)
	}
	if len(local.queue) != 2 || local.queue[0].Type != "resize" || local.queue[1].Type != "visibility" || local.queue[1].Visible == nil || !*local.queue[1].Visible {
		t.Fatalf("Local viewer was not restored: %+v", local.queue)
	}
}

func TestMachineCatalogRemovalCancelsPendingSwitchAndOwnedAttempt(t *testing.T) {
	t.Setenv("IKESU_STATE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := &managedEndpoint{profile: machine.Profile{ID: "remote", Enabled: true}, cancel: cancel}
	m := &machineClient{session: "catalog-test", active: "local", endpoints: map[string]*managedEndpoint{"local": {}, "remote": remote}, pending: &pendingMachine{id: "remote", deadline: time.Now()}}
	if err := os.MkdirAll(filepath.Dir(m.catalogPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.catalogPath(), []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshMachineCatalog(); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || m.pending != nil || m.active != "local" || m.endpoints["remote"] != nil {
		t.Fatal("removed endpoint retained a switch/attempt or changed Local selection")
	}
	m.advance(time.Now())
}

func TestMachineCatalogInvalidJSONKeepsConnectionsAndSelection(t *testing.T) {
	t.Setenv("IKESU_STATE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := &managedEndpoint{profile: machine.Profile{ID: "remote", Enabled: true}, cancel: cancel}
	m := &machineClient{session: "catalog-test", active: "remote", order: []string{"local", "remote"}, endpoints: map[string]*managedEndpoint{"local": {}, "remote": remote}}
	if err := os.MkdirAll(filepath.Dir(m.catalogPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.catalogPath(), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshMachineCatalog(); err == nil {
		t.Fatal("invalid catalog silently replaced saved machines")
	}
	if ctx.Err() != nil || m.active != "remote" || m.endpoints["remote"] != remote || len(m.order) != 2 {
		t.Fatal("invalid catalog changed selected endpoint or stopped its owned attempt")
	}
}
