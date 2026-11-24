package server

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/teststate"
)

func TestDecisionProjectionFanoutAndInvalidation(t *testing.T) {
	teststate.Lock(t)
	s, _, _ := screenTerminal(t, "claude-single", "claude")
	// Open the isolated store and initialize the projection before adding clients.
	s.app.Do(func() {
		if _, err := s.db(); err != nil {
			t.Error(err)
			return
		}
		if _, err := s.decisionProjection(); err != nil {
			t.Error(err)
		}
	})
	s.mu.Lock()
	s.clients = make(map[*clientConn]bool)
	s.mu.Unlock()
	var readers []*json.Decoder
	var clients []*clientConn
	for range 2 {
		conn, peer := net.Pipe()
		c := &clientConn{conn: conn, available: endpoint.DecisionGeneration}
		c.startWriter()
		t.Cleanup(func() {
			_ = peer.Close()
			c.close()
		})
		if err := peer.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		s.clients[c] = true
		s.mu.Unlock()
		clients = append(clients, c)
		readers = append(readers, json.NewDecoder(peer))
	}

	var lastRevision uint64
	check := func(wantID string) {
		t.Helper()
		var shared *endpoint.DecisionProjection
		for _, c := range clients {
			c.qmu.Lock()
			var projection *endpoint.DecisionProjection
			if len(c.queue) > 0 {
				projection = c.queue[len(c.queue)-1].m.Decisions
			}
			c.qmu.Unlock()
			if projection == nil || shared != nil && projection != shared {
				t.Fatal("clients did not receive the same queued projection")
			}
			shared = projection
		}
		var first *endpoint.DecisionProjection
		for _, reader := range readers {
			var msg clientMsg
			if err := reader.Decode(&msg); err != nil {
				t.Fatal(err)
			}
			got := msg.Decisions
			if msg.Type != "decisions" || got == nil || got.BootID == "" || got.Revision <= lastRevision {
				t.Fatalf("invalid projection: %+v", msg)
			}
			if first == nil {
				first = got
			} else if !reflect.DeepEqual(first, got) {
				t.Fatalf("fanout differs: %+v vs %+v", first, got)
			}
			if wantID == "" && len(got.Pending) != 0 || wantID != "" && (len(got.Pending) != 1 || got.Pending[0].DecisionID != wantID) {
				t.Fatalf("pending = %+v, want %q", got.Pending, wantID)
			}
		}
		lastRevision = first.Revision
	}
	call := func(method string, params any) api.Decision {
		t.Helper()
		var resp api.Response
		s.app.Do(func() {
			resp = s.decisionRequest(&api.Request{ID: method, Method: method, Params: api.MarshalParams(params)})
		})
		if resp.Err != nil {
			t.Fatal(resp.Err)
		}
		var result api.DecisionResult
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatal(err)
		}
		return result.Decision
	}

	params := api.DecisionCreateParams{Kind: "ask", Title: "Review?", Options: []api.DecisionOption{{ID: "yes", Label: "Yes", Role: "approve"}}}
	answered := call(api.MethodDecisionCreate, params)
	check(answered.DecisionID)
	s.app.Do(func() {
		cached := &s.decisions.pendingCache[0]
		s.syncDecisionAttention()
		copy, err := s.pendingDecisionsCached()
		if err != nil || len(copy) != 1 {
			t.Errorf("warm cache returned %d rows, err=%v", len(copy), err)
			return
		}
		copy[0].LateAnswer = true
		projection, err := s.decisionProjection()
		if err != nil || len(projection.Pending) != 1 {
			t.Errorf("warm projection invalid: %+v, err=%v", projection, err)
			return
		}
		if cached != &s.decisions.pendingCache[0] || cached.LateAnswer || projection.Pending[0].LateAnswer {
			t.Error("warm attention and projection must reuse the cache without modifying cached rows")
		}
	})
	yes := "yes"
	call(api.MethodDecisionAnswer, api.DecisionAnswerParams{DecisionID: answered.DecisionID, OptionID: &yes})
	check("")
	cancelled := call(api.MethodDecisionCreate, params)
	check(cancelled.DecisionID)
	call(api.MethodDecisionCancel, api.DecisionTargetParams{DecisionID: cancelled.DecisionID})
	check("")

	// Seed a due record without racing a real timer.
	var due api.Decision
	s.app.Do(func() {
		timeout := uint64(1)
		params.TimeoutMS = &timeout
		var err error
		due, err = s.store.CreateDecision(params, time.Now().Add(-time.Minute))
		if err != nil {
			t.Error(err)
			return
		}
		s.decisionChanged("decision_created", due)
	})
	check(due.DecisionID)
	s.app.Do(func() {
		if err := s.expireDecisions(s.store); err != nil {
			t.Error(err)
		}
	})
	check("")
	got, err := s.store.Decision(due.DecisionID)
	if err != nil || got.Status != "expired" {
		t.Fatalf("expiry did not reach SQLite: %+v, %v", got, err)
	}
}
