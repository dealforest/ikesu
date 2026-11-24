package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestPendingDecisionCacheDetectsSharedDBUpdates(t *testing.T) {
	teststate.Lock(t)
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	teststate.Setenv(t, "IKESU_DB_PATH", path)
	s := newTestServer(t, "shared")
	store, err := s.db()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := dispatch.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	s.initDecisionHub()
	defer s.stopDecisions()

	option := "yes"
	params := api.DecisionCreateParams{Kind: "ask", Title: "Shared decision", Options: []api.DecisionOption{{ID: option, Label: "Yes", Role: "approve"}}}
	createDue := func() api.Decision {
		t.Helper()
		withTimeout := params
		timeout := uint64(10)
		withTimeout.TimeoutMS = &timeout
		d, err := store.CreateDecision(withTimeout, time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		s.invalidatePendingDecisions()
		rows, err := s.pendingDecisionsCached()
		if err != nil || len(rows) != 1 || rows[0].DecisionID != d.DecisionID {
			t.Fatalf("pending cache = %+v, err=%v, want %s", rows, err, d.DecisionID)
		}
		return d
	}
	create := func() api.Decision {
		t.Helper()
		resp := s.decisionRequest(&api.Request{Method: api.MethodDecisionCreate, Params: api.MarshalParams(params)})
		if resp.Err != nil {
			t.Fatal(resp.Err)
		}
		var out api.DecisionResult
		if err := json.Unmarshal(resp.Result, &out); err != nil {
			t.Fatal(err)
		}
		return out.Decision
	}
	checkProjection := func(want string) {
		t.Helper()
		p, err := s.decisionProjection()
		if err != nil {
			t.Fatal(err)
		}
		if want == "" && len(p.Pending) != 0 || want != "" && (len(p.Pending) != 1 || p.Pending[0].DecisionID != want) {
			t.Fatalf("projection = %+v, want pending %q", p.Pending, want)
		}
	}
	checkProjection("") // Initialize the projection before seeding past-deadline rows.
	answered := createDue()
	if _, err := other.AnswerDecision(api.DecisionAnswerParams{DecisionID: answered.DecisionID, OptionID: &option}, time.Now()); err != nil {
		t.Fatal(err)
	}
	checkProjection("")

	cancelled := createDue()
	if _, err := other.CancelDecision(cancelled.DecisionID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Exercise the expiry preflight independently of a projection refresh.
	next := create()
	checkProjection(next.DecisionID)
	if _, err := store.CancelDecision(next.DecisionID, time.Now()); err != nil {
		t.Fatal(err)
	}

	stale := createDue()
	// Same-connection commits do not change data_version. This leaves the
	// exact stale view that an external commit between the read and the expiry
	// write would produce.
	if _, err := store.CancelDecision(stale.DecisionID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.expireDecisions(store); err != nil {
		t.Fatal(err)
	}
	if s.decisions.pendingCacheValid {
		t.Fatal("conflicting expiry left the stale cache valid")
	}
	checkProjection(create().DecisionID)
}
