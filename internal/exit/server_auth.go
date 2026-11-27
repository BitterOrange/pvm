package exit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// CreateInviteRequest asks an admin token for a one-time invite.
type CreateInviteRequest struct {
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"` // default enroll
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	MaxPeers   *int   `json:"max_peers,omitempty"` // default DefaultMaxPeers; 0 = no limit
}

// CreateInviteResponse returns the invite secret once; the server keeps
// only its hash.
type CreateInviteResponse struct {
	InviteInfo
	Secret string `json:"secret"`
}

// RedeemRequest trades an invite secret for a token.
type RedeemRequest struct {
	Secret string `json:"secret"`
}

// RedeemResponse returns the issued token once; the server keeps only its
// hash.
type RedeemResponse struct {
	TokenInfo
	Token string `json:"token"`
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, _ principal) {
	var req CreateInviteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = RoleEnroll
	}
	ttl := DefaultInviteTTL
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	maxPeers := DefaultMaxPeers
	if req.MaxPeers != nil {
		maxPeers = *req.MaxPeers
	}
	if req.Role == RoleAdmin {
		maxPeers = 0
	}
	inv, secret, err := s.auth.createInvite(req.Name, req.Role, ttl, maxPeers, time.Now())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	writeJSON(w, CreateInviteResponse{InviteInfo: inv, Secret: secret})
}

func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	var req RedeemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Secret == "" {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	tok, secret, err := s.auth.redeem(req.Secret, time.Now())
	if errors.Is(err, errInvalidInvite) {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, `{"error":"store"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, RedeemResponse{TokenInfo: tok, Token: secret})
}

func (s *Server) handleListInvites(w http.ResponseWriter, _ *http.Request, _ principal) {
	writeJSON(w, map[string]any{"invites": s.auth.listInvites(time.Now())})
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request, _ principal) {
	ok, err := s.auth.removeInvite(r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"error":"store"}`, http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"removed": r.PathValue("id")})
}

func (s *Server) handleListTokens(w http.ResponseWriter, _ *http.Request, _ principal) {
	writeJSON(w, map[string]any{"tokens": s.auth.listTokens()})
}

// handleDeleteToken revokes a token and, unless keep_peers=1, removes the
// peers it registered, so a revoked client loses the data plane too.
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request, _ principal) {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	if s.isClosed() {
		http.Error(w, `{"error":"server closed"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	_, ok, err := s.auth.removeToken(id)
	if err != nil {
		http.Error(w, `{"error":"store"}`, http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	removed := 0
	if r.URL.Query().Get("keep_peers") != "1" {
		for _, p := range s.store.List() {
			if p.Owner != id {
				continue
			}
			if err := s.removePeerLocked(p); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"token revoked, but removing its peer %s failed after %d removals: %s"}`, p.IP, removed, err), http.StatusInternalServerError)
				return
			}
			removed++
		}
	}
	writeJSON(w, map[string]any{"removed": id, "peers_removed": removed})
}
