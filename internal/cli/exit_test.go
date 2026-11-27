package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huangzheng2016/pvm/internal/exit"
	"github.com/huangzheng2016/pvm/internal/tunnel"
)

func TestObservedIPMatchesHost(t *testing.T) {
	if ok, err := observedIPMatchesHost("192.0.2.1", "192.0.2.1", nil); err != nil || !ok {
		t.Fatalf("literal ip should match: %v, %v", ok, err)
	}
	resolve := func(host string) ([]net.IP, error) {
		if host != "exit.example" {
			t.Fatalf("unexpected host %q", host)
		}
		return []net.IP{net.ParseIP("192.0.2.1"), net.ParseIP("2001:db8::1")}, nil
	}
	if ok, err := observedIPMatchesHost("exit.example", "2001:db8::1", resolve); err != nil || !ok {
		t.Fatalf("one of A/AAAA should match: %v, %v", ok, err)
	}
	if ok, err := observedIPMatchesHost("exit.example", "192.0.2.99", resolve); err != nil || ok {
		t.Fatalf("different ip must not match: %v, %v", ok, err)
	}
	if _, err := observedIPMatchesHost("exit.example", "192.0.2.1", func(string) ([]net.IP, error) {
		return nil, errors.New("dns down")
	}); err == nil {
		t.Fatal("resolver error must be returned")
	}
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns the
// exit code plus everything printed.
func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	code := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return code, string(out)
}

// exitTest against a real in-process exit-agent (userspace NAT): handshake,
// TCP /ping, UDP probe, and ICMP probe must all report ok without touching
// any third-party service.
func TestExitTestOwnAgentProbes(t *testing.T) {
	ownAgentEntry(t)
	code, out := captureStdout(t, func() int { return exitTest([]string{"my-exit"}) })
	if code != 0 {
		t.Fatalf("exitTest code = %d, output:\n%s", code, out)
	}
	for _, want := range []string{
		"wireguard handshake: ok",
		"tcp /ping: ok",
		"udp probe: ok",
		"icmp probe: ok",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ifconfig.me") {
		t.Errorf("output still references ifconfig.me:\n%s", out)
	}
}

// An entry whose tunnel ip went stale (the agent re-enrolled its key from
// a copy) handshakes but gets no data back; exitTest reads the ip back,
// saves it and passes.
func TestExitTestStaleTunnelIPReEnrolls(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out one tunnel http timeout")
	}
	e, dir := ownAgentEntry(t)
	want := e.TunnelIP
	e.TunnelIP = "10.99.0.200"
	if err := exit.SaveEntry(dir, e); err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int { return exitTest([]string{"my-exit"}) })
	if code != 0 {
		t.Fatalf("exitTest code = %d, output:\n%s", code, out)
	}
	for _, s := range []string{"another tunnel ip; entry updated, retrying", "tcp /ping: ok", "icmp probe: ok"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	got, err := exit.LoadEntry(dir, "my-exit")
	if err != nil {
		t.Fatal(err)
	}
	if got.TunnelIP != want {
		t.Fatalf("saved tunnel ip = %s, want the agent's %s", got.TunnelIP, want)
	}
}

// ownAgentEntry starts an in-process exit-agent (userspace NAT), enrolls a
// key and saves it as exit "my-exit" in a fresh PVM_HOME.
func ownAgentEntry(t *testing.T) (*exit.Entry, string) {
	t.Helper()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	wgPort := ln.LocalAddr().(*net.UDPAddr).Port
	ln.Close()

	srv, err := exit.NewServer(exit.ServerConfig{
		Listen:     "127.0.0.1:0",
		Token:      "test-token",
		StateDir:   t.TempDir(),
		UDPPort:    wgPort,
		Transports: "udp",
		NAT:        exit.NATUserspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	fp, err := exit.ProbeFingerprint(srv.ControlAddr())
	if err != nil {
		t.Fatal(err)
	}
	priv, err := tunnel.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := tunnel.PublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := exit.NewClient(srv.ControlAddr(), "test-token", fp).Enroll(ctx, pub, "cli-test", "udp")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("PVM_HOME", home)
	dir := filepath.Join(home, "exits")
	e := &exit.Entry{
		Name:         "my-exit",
		Server:       srv.ControlAddr(),
		Token:        "test-token",
		Transport:    "udp",
		Fingerprint:  fp,
		WGPrivateKey: priv,
		WGPublicKey:  pub,
		ServerPubKey: resp.ServerPubKey,
		TunnelIP:     resp.TunnelIP,
		ServerIP:     resp.ServerIP,
		MTU:          resp.MTU,
		Endpoint:     resp.Endpoint,
	}
	if err := exit.SaveEntry(dir, e); err != nil {
		t.Fatal(err)
	}
	return e, dir
}

// The documented form puts the name before --file; flags-first works too.
func TestExitImportArgs(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, args := range [][]string{
		{"my-sub", "--file", missing, "--select", "NODE"},
		{"--file", missing, "my-sub"},
	} {
		out := captureStderr(t, func() { exitImport(args) })
		if !strings.Contains(out, "read proxy config") {
			t.Errorf("%v did not reach the file read: %q", args, out)
		}
	}
	for _, args := range [][]string{
		{"my-sub"},
		{"--file", missing},
		{"my-sub", "--file", missing, "extra"},
	} {
		out := captureStderr(t, func() { exitImport(args) })
		if !strings.Contains(out, "usage: pvm exit import") {
			t.Errorf("%v: want usage, got %q", args, out)
		}
	}
}

// An invite adds an exit with a fresh enroll token instead of the admin's,
// and spends itself on first use.
func TestExitAddWithInvite(t *testing.T) {
	admin, dir := ownAgentEntry(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ac := exit.NewClient(admin.Server, admin.Token, admin.Fingerprint)
	created, err := ac.CreateInvite(ctx, exit.CreateInviteRequest{Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	invite := exit.Invite{Server: admin.Server, Fingerprint: admin.Fingerprint, Secret: created.Secret}.String()

	if code := exitAdd([]string{"x", "--invite", invite, "--server", admin.Server}); code == 0 {
		t.Fatal("--invite with --server must be refused")
	}
	code, out := captureStdout(t, func() int {
		return exitAdd([]string{"new-exit", "--invite", invite, "--transport", "udp"})
	})
	if code != 0 {
		t.Fatalf("exit add --invite code = %d, output:\n%s", code, out)
	}
	for _, want := range []string{"invite redeemed", "(laptop, role enroll)", "matches the invite; pinned"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	e, err := exit.LoadEntry(dir, "new-exit")
	if err != nil {
		t.Fatal(err)
	}
	if e.Token == "" || e.Token == admin.Token || e.Fingerprint != admin.Fingerprint {
		t.Fatalf("entry must hold its own token and the invite's fingerprint: %+v", e)
	}
	peers, err := exit.NewClient(e.Server, e.Token, e.Fingerprint).ListPeers(ctx)
	if err != nil || len(peers) != 1 || peers[0].IP != e.TunnelIP {
		t.Fatalf("enroll token must see exactly its own peer, got %+v, %v", peers, err)
	}
	if code := exitAdd([]string{"again", "--invite", invite, "--transport", "udp"}); code == 0 {
		t.Fatal("a used invite must not add another exit")
	}
	if _, err := exit.LoadEntry(dir, "again"); err == nil {
		t.Fatal("a failed invite must not leave an entry")
	}

	// --invite-file, as PVM Desktop passes it
	created, err = ac.CreateInvite(ctx, exit.CreateInviteRequest{Name: "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "invite")
	if err := os.WriteFile(f, []byte(exit.Invite{Server: admin.Server, Fingerprint: admin.Fingerprint, Secret: created.Secret}.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := exitAdd([]string{"x", "--invite-file", f, "--invite", invite}); code == 0 {
		t.Fatal("--invite-file with --invite must be refused")
	}
	if code, out := captureStdout(t, func() int {
		return exitAdd([]string{"from-file", "--invite-file", f, "--transport", "udp"})
	}); code != 0 || !strings.Contains(out, "(desktop, role enroll)") {
		t.Fatalf("exit add --invite-file code = %d, output:\n%s", code, out)
	}
}
