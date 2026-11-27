package exit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangzheng2016/pvm/internal/tunnel"
)

func TestAuthStoreInviteRedeemsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	s, err := loadAuthStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	inv, secret, err := s.createInvite("laptop", RoleEnroll, time.Minute, 4, now)
	if err != nil {
		t.Fatal(err)
	}
	tok, token, err := s.redeem(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "laptop" || tok.Role != RoleEnroll || tok.MaxPeers != 4 || tok.ID == inv.ID {
		t.Fatalf("issued token = %+v", tok)
	}
	if _, _, err := s.redeem(secret, now); !errors.Is(err, errInvalidInvite) {
		t.Fatalf("second redeem err = %v, want errInvalidInvite", err)
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disk), secret) || strings.Contains(string(disk), token) {
		t.Fatal("auth.json must hold only hashes of invite and token secrets")
	}
	reloaded, err := loadAuthStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := reloaded.lookup(token); !ok || got.ID != tok.ID {
		t.Fatalf("issued token not found after reload: %+v %v", got, ok)
	}
	if len(reloaded.listInvites(now)) != 0 {
		t.Fatal("a redeemed invite must be gone")
	}
}

func TestAuthStoreInviteExpiresAndValidates(t *testing.T) {
	s, err := loadAuthStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_, secret, err := s.createInvite("laptop", RoleEnroll, time.Minute, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.redeem(secret, now.Add(time.Minute)); !errors.Is(err, errInvalidInvite) {
		t.Fatalf("expired redeem err = %v", err)
	}
	if _, _, err := s.redeem("not-a-secret", now); !errors.Is(err, errInvalidInvite) {
		t.Fatalf("unknown redeem err = %v", err)
	}
	for _, c := range []struct {
		name, role string
		ttl        time.Duration
		max        int
	}{
		{"bad name!", RoleEnroll, time.Minute, 0},
		{"laptop", "root", time.Minute, 0},
		{"laptop", RoleEnroll, 0, 0},
		{"laptop", RoleEnroll, MaxInviteTTL + time.Second, 0},
		{"laptop", RoleEnroll, time.Minute, -1},
	} {
		if _, _, err := s.createInvite(c.name, c.role, c.ttl, c.max, now); err == nil {
			t.Errorf("createInvite(%+v) must fail", c)
		}
	}
}

func TestInviteRoundTrip(t *testing.T) {
	inv := Invite{Server: "vps.example.com:18443", Fingerprint: strings.Repeat("ab", 32), Secret: strings.Repeat("A", 43)}
	got, err := ParseInvite("  " + inv.String() + "\n")
	if err != nil || got != inv {
		t.Fatalf("ParseInvite = %+v, %v; want %+v", got, err, inv)
	}
	for _, bad := range []string{
		"YOUR_TOKEN",
		"pvmx1:!!!",
		Invite{Server: "vps.example.com", Fingerprint: inv.Fingerprint, Secret: inv.Secret}.String(),
		Invite{Server: inv.Server, Fingerprint: "abcd", Secret: inv.Secret}.String(),
		Invite{Server: inv.Server, Fingerprint: inv.Fingerprint, Secret: "short"}.String(),
	} {
		if _, err := ParseInvite(bad); err == nil {
			t.Errorf("ParseInvite(%q) must fail", bad)
		}
	}
}

// The whole role model against a live agent: an admin invite redeems into
// an enroll token that registers peers, sees and removes only its own,
// stays within its peer limit, cannot reach admin requests, and loses its
// peers when revoked.
func TestEnrollTokenScopeEndToEnd(t *testing.T) {
	srv, _ := inProcessServer(t, "udp")
	addr := srv.ControlAddr()
	fp, err := ProbeFingerprint(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
	defer cancel()
	admin := NewClient(addr, "test-token", fp)
	newKey := func() string {
		t.Helper()
		priv, err := tunnel.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		pub, err := tunnel.PublicKey(priv)
		if err != nil {
			t.Fatal(err)
		}
		return pub
	}
	adminKey := newKey()
	adminPeer, err := admin.Enroll(ctx, adminKey, "admin-host", "udp")
	if err != nil {
		t.Fatal(err)
	}

	two := 2
	inv, err := admin.CreateInvite(ctx, CreateInviteRequest{Name: "laptop", MaxPeers: &two})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Role != RoleEnroll || inv.MaxPeers != 2 {
		t.Fatalf("invite = %+v", inv.InviteInfo)
	}
	if _, err := NewClient(addr, "", strings.Repeat("00", 32)).Redeem(ctx, inv.Secret); err == nil {
		t.Fatal("redeem must refuse a server whose fingerprint differs from the pin")
	}
	red, err := NewClient(addr, "", fp).Redeem(ctx, inv.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(addr, "", fp).Redeem(ctx, inv.Secret); err == nil {
		t.Fatal("an invite must redeem only once")
	}
	user := NewClient(addr, red.Token, fp)

	if _, err := user.Enroll(ctx, adminKey, "steal", "udp"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("enrolling the admin's key must be refused, got %v", err)
	}
	own1, err := user.Enroll(ctx, newKey(), "laptop", "udp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := user.Enroll(ctx, newKey(), "laptop-gw", "udp"); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Enroll(ctx, newKey(), "third", "udp"); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("a third peer must exceed max_peers=2, got %v", err)
	}
	peers, err := user.ListPeers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("enroll token must list only its 2 peers, got %d", len(peers))
	}
	if err := user.DeletePeer(ctx, adminPeer.TunnelIP); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("deleting the admin's peer must look absent, got %v", err)
	}
	if _, err := user.CreateInvite(ctx, CreateInviteRequest{Name: "x"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("an enroll token must not create invites, got %v", err)
	}
	if _, err := user.ListTokens(ctx); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("an enroll token must not list tokens, got %v", err)
	}
	if err := user.DeletePeer(ctx, own1.TunnelIP); err != nil {
		t.Fatalf("an enroll token must remove its own peer: %v", err)
	}

	toks, err := admin.ListTokens(ctx)
	if err != nil || len(toks) != 1 || toks[0].ID != red.ID {
		t.Fatalf("admin token list = %+v, %v", toks, err)
	}
	n, err := admin.DeleteToken(ctx, red.ID, false)
	if err != nil || n != 1 {
		t.Fatalf("revoke removed %d peers, err %v; want 1", n, err)
	}
	if _, err := user.ListPeers(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a revoked token must be unauthorized, got %v", err)
	}
	all, err := admin.ListPeers(ctx)
	if err != nil || len(all) != 1 || all[0].IP != adminPeer.TunnelIP {
		t.Fatalf("after revoke only the admin peer must remain, got %+v, %v", all, err)
	}
}

// `pvm exit rm` revokes the host's token only once its last peer is gone,
// and never an admin token.
func TestRevokeSelf(t *testing.T) {
	srv, _ := inProcessServer(t, "udp")
	addr := srv.ControlAddr()
	fp, err := ProbeFingerprint(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
	defer cancel()
	admin := NewClient(addr, "test-token", fp)
	if revoked, reason, err := admin.RevokeSelf(ctx); err != nil || revoked || reason != "admin token" {
		t.Fatalf("admin self-revoke = %v %q %v", revoked, reason, err)
	}
	inv, err := admin.CreateInvite(ctx, CreateInviteRequest{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	red, err := NewClient(addr, "", fp).Redeem(ctx, inv.Secret)
	if err != nil {
		t.Fatal(err)
	}
	user := NewClient(addr, red.Token, fp)
	priv, _ := tunnel.GenerateKey()
	pub, _ := tunnel.PublicKey(priv)
	peer, err := user.Enroll(ctx, pub, "laptop", "udp")
	if err != nil {
		t.Fatal(err)
	}
	if revoked, _, err := user.RevokeSelf(ctx); err != nil || revoked {
		t.Fatalf("a token with a peer must be kept: %v %v", revoked, err)
	}
	if err := user.DeletePeer(ctx, peer.TunnelIP); err != nil {
		t.Fatal(err)
	}
	if revoked, _, err := user.RevokeSelf(ctx); err != nil || !revoked {
		t.Fatalf("a token without peers must be revoked: %v %v", revoked, err)
	}
	if _, err := user.ListPeers(ctx); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the revoked token must be unauthorized, got %v", err)
	}
	if toks, err := admin.ListTokens(ctx); err != nil || len(toks) != 0 {
		t.Fatalf("tokens after self-revoke = %+v, %v", toks, err)
	}
}
