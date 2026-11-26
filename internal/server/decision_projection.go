package server

import (
	"crypto/rand"
	"encoding/hex"
	"slices"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/endpoint"
)

// invalidatePendingDecisions marks the app-loop-owned projection cache dirty.
func (s *Server) invalidatePendingDecisions() {
	s.decisions.pendingCacheValid = false
}

// pendingDecisionsCached returns a separate slice for projection overlays.
func (s *Server) pendingDecisionsCached() ([]api.Decision, error) {
	if s.loadedStore() == nil {
		return []api.Decision{}, nil
	}
	// Sample before reading the rows. If a commit interleaves with the read,
	// the next lookup sees the changed version and refreshes again.
	version, versionErr := s.store.DataVersion()
	if versionErr != nil || version != s.decisions.pendingCacheDataVersion {
		s.invalidatePendingDecisions()
	}
	if !s.decisions.pendingCacheValid {
		rows, err := s.store.Decisions("pending")
		if err != nil {
			return nil, err
		}
		s.decisions.pendingCache = rows
		s.decisions.pendingCacheDataVersion = version
		s.decisions.pendingCacheValid = versionErr == nil
	}
	out := make([]api.Decision, len(s.decisions.pendingCache))
	copy(out, s.decisions.pendingCache)
	return out, nil
}

func (s *Server) decisionProjection() (*endpoint.DecisionProjection, error) {
	s.initDecisionHub()
	if s.decisions.boot == "" {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, err
		}
		s.decisions.boot = hex.EncodeToString(bytes[:])
		s.decisions.revision = 1
		if dispatch.DBExists(s.session) {
			store, err := s.db()
			if err != nil {
				return nil, err
			}
			if err := s.expireDecisions(store); err != nil {
				return nil, err
			}
		}
	}
	pending, err := s.pendingDecisionsCached()
	if err != nil {
		return nil, err
	}
	for i := range pending {
		pending[i].LateAnswer = s.screenPrompts.late[pending[i].DecisionID] != nil
	}
	return &endpoint.DecisionProjection{BootID: s.decisions.boot, Revision: s.decisions.revision, Pending: pending}, nil
}
func (s *Server) sendDecisionProjection(c *clientConn) {
	if c.available < endpoint.DecisionGeneration {
		return
	}
	projection, err := s.decisionProjection()
	c.sendDecisionProjection(projection, err)
}

func (c *clientConn) sendDecisionProjection(projection *endpoint.DecisionProjection, err error) {
	if err != nil {
		c.post(clientMsg{Type: "error", Reason: "decision projection: " + err.Error()})
		return
	}
	c.post(clientMsg{Type: "decisions", Decisions: projection})
}
func (s *Server) pushDecisionProjection() {
	s.decisions.revision++
	clients := s.displayClients()
	if !slices.ContainsFunc(clients, func(c *clientConn) bool {
		return c.available >= endpoint.DecisionGeneration
	}) {
		return
	}
	projection, err := s.decisionProjection()
	for _, c := range clients {
		if c.available >= endpoint.DecisionGeneration {
			c.sendDecisionProjection(projection, err)
		}
	}
}
