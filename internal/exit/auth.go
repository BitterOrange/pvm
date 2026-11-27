package exit

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/huangzheng2016/pvm/internal/fsutil"
)

// Roles of control-plane tokens. An admin token manages every peer, token
// and invite; an enroll token registers peers and sees or removes only the
// peers it registered.
const (
	RoleAdmin  = "admin"
	RoleEnroll = "enroll"
)

const (
	// DefaultInviteTTL bounds how long an unredeemed invite stays valid.
	DefaultInviteTTL = 15 * time.Minute
	// MaxInviteTTL caps --ttl so a forgotten invite cannot linger.
	MaxInviteTTL = 24 * time.Hour
	// DefaultMaxPeers bounds the peers one enroll token may hold: the host
	// entry, a gateway per host and short-lived probe peers fit easily,
	// while a leaked token cannot drain the /16 pool.
	DefaultMaxPeers = 64
)

// configAdminID names the admin token read from the token file; it is not
// stored in auth.json and cannot be revoked through the API.
const configAdminID = "admin"

var tokenNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidTokenName reports whether name may label a token or invite.
func ValidTokenName(name string) bool { return tokenNameRE.MatchString(name) }

// TokenInfo describes an issued token without its secret.
type TokenInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	MaxPeers  int       `json:"max_peers,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// InviteInfo describes a pending invite without its secret.
type InviteInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	MaxPeers  int       `json:"max_peers,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type tokenRecord struct {
	TokenInfo
	Hash string `json:"sha256"`
}

type inviteRecord struct {
	InviteInfo
	Hash string `json:"sha256"`
}

// principal is the authenticated caller of a control-plane request.
type principal struct {
	ID       string
	Role     string
	MaxPeers int
}

// owner is the peer owner recorded for p: admin peers stay unowned, so the
// peers enrolled before roles existed keep belonging to the admin.
func (p principal) owner() string {
	if p.Role == RoleAdmin {
		return ""
	}
	return p.ID
}

// authStore persists issued tokens and pending invites in auth.json. Only
// SHA-256 hashes of the secrets are written, so the state directory alone
// does not grant access.
type authStore struct {
	mu      sync.Mutex
	path    string
	Tokens  []tokenRecord  `json:"tokens"`
	Invites []inviteRecord `json:"invites"`
}

func loadAuthStore(path string) (*authStore, error) {
	s := &authStore{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func (s *authStore) saveLocked() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.path, b, 0o600)
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// lookup returns the token whose hash matches secret. Every record is
// compared so the time taken does not depend on which one matches.
func (s *authStore) lookup(secret string) (tokenRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := []byte(hashSecret(secret))
	var found tokenRecord
	ok := false
	for _, t := range s.Tokens {
		if subtle.ConstantTimeCompare(h, []byte(t.Hash)) == 1 {
			found, ok = t, true
		}
	}
	return found, ok
}

func validRole(role string) bool { return role == RoleAdmin || role == RoleEnroll }

// createInvite records a pending invite and returns it with its secret.
func (s *authStore) createInvite(name, role string, ttl time.Duration, maxPeers int, now time.Time) (InviteInfo, string, error) {
	if !ValidTokenName(name) {
		return InviteInfo{}, "", fmt.Errorf("invalid name %q (1-64 letters, digits, '.', '_' or '-')", name)
	}
	if !validRole(role) {
		return InviteInfo{}, "", fmt.Errorf("invalid role %q (admin|enroll)", role)
	}
	if ttl <= 0 || ttl > MaxInviteTTL {
		return InviteInfo{}, "", fmt.Errorf("invite ttl must be within (0, %s]", MaxInviteTTL)
	}
	if maxPeers < 0 {
		return InviteInfo{}, "", fmt.Errorf("max peers must not be negative")
	}
	secret, err := randomSecret()
	if err != nil {
		return InviteInfo{}, "", err
	}
	id, err := randomID()
	if err != nil {
		return InviteInfo{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	inv := inviteRecord{
		InviteInfo: InviteInfo{ID: id, Name: name, Role: role, MaxPeers: maxPeers, CreatedAt: now, ExpiresAt: now.Add(ttl)},
		Hash:       hashSecret(secret),
	}
	before := s.Invites
	s.Invites = append(append([]inviteRecord(nil), s.Invites...), inv)
	if err := s.saveLocked(); err != nil {
		s.Invites = before
		return InviteInfo{}, "", err
	}
	return inv.InviteInfo, secret, nil
}

var errInvalidInvite = errors.New("invalid, used or expired invite")

// redeem consumes the invite matching secret and issues its token. The
// invite and the token change in one write, so a secret redeems once.
func (s *authStore) redeem(secret string, now time.Time) (TokenInfo, string, error) {
	h := []byte(hashSecret(secret))
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, inv := range s.Invites {
		if subtle.ConstantTimeCompare(h, []byte(inv.Hash)) == 1 {
			idx = i
		}
	}
	if idx < 0 || !now.Before(s.Invites[idx].ExpiresAt) {
		return TokenInfo{}, "", errInvalidInvite
	}
	inv := s.Invites[idx]
	token, err := randomSecret()
	if err != nil {
		return TokenInfo{}, "", err
	}
	id, err := randomID()
	if err != nil {
		return TokenInfo{}, "", err
	}
	rec := tokenRecord{
		TokenInfo: TokenInfo{ID: id, Name: inv.Name, Role: inv.Role, MaxPeers: inv.MaxPeers, CreatedAt: now},
		Hash:      hashSecret(token),
	}
	beforeInv, beforeTok := s.Invites, s.Tokens
	s.Invites = append(append([]inviteRecord(nil), s.Invites[:idx]...), s.Invites[idx+1:]...)
	s.Tokens = append(append([]tokenRecord(nil), s.Tokens...), rec)
	s.pruneLocked(now)
	if err := s.saveLocked(); err != nil {
		s.Invites, s.Tokens = beforeInv, beforeTok
		return TokenInfo{}, "", err
	}
	return rec.TokenInfo, token, nil
}

// pruneLocked drops expired invites; it is persisted with the next write.
func (s *authStore) pruneLocked(now time.Time) {
	kept := s.Invites[:0:0]
	for _, inv := range s.Invites {
		if now.Before(inv.ExpiresAt) {
			kept = append(kept, inv)
		}
	}
	s.Invites = kept
}

func (s *authStore) listTokens() []TokenInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TokenInfo, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		out = append(out, t.TokenInfo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *authStore) listInvites(now time.Time) []InviteInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []InviteInfo{}
	for _, inv := range s.Invites {
		if now.Before(inv.ExpiresAt) {
			out = append(out, inv.InviteInfo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *authStore) removeToken(id string) (TokenInfo, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.Tokens {
		if t.ID == id {
			before := s.Tokens
			s.Tokens = append(append([]tokenRecord(nil), s.Tokens[:i]...), s.Tokens[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.Tokens = before
				return TokenInfo{}, false, err
			}
			return t.TokenInfo, true, nil
		}
	}
	return TokenInfo{}, false, nil
}

func (s *authStore) removeInvite(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, inv := range s.Invites {
		if inv.ID == id {
			before := s.Invites
			s.Invites = append(append([]inviteRecord(nil), s.Invites[:i]...), s.Invites[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.Invites = before
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}
