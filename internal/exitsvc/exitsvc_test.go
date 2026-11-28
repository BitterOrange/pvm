package exitsvc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangzheng2016/pvm/internal/exit"
)

func TestDefaultConfigRoundTrips(t *testing.T) {
	for _, mode := range []Mode{ModeSystem, ModeUser} {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		d, err := DirsFor(mode)
		if err != nil {
			t.Fatal(err)
		}
		c := DefaultConfig(d)
		got, err := ParseConfig(c.Marshal())
		if err != nil {
			t.Fatalf("%s default config: %v", mode, err)
		}
		if got.StateDir != d.State || got.AdminTokenFile != filepath.Join(d.Config, "admin.token") {
			t.Fatalf("%s paths = %+v", mode, got)
		}
		if mode == ModeUser && got.NAT != exit.NATUserspace {
			t.Fatalf("user mode must default to userspace NAT, got %s", got.NAT)
		}
	}
}

func TestParseConfigRejects(t *testing.T) {
	d, _ := DirsFor(ModeSystem)
	good := string(DefaultConfig(d).Marshal())
	for name, doc := range map[string]string{
		"unknown key": good + "lsiten: :1\n",
		"bad nat":     strings.Replace(good, "nat: auto", "nat: pf", 1),
		"bad port":    strings.Replace(good, "udp_port: 18443", "udp_port: 70000", 1),
		"relative":    strings.Replace(good, "state_dir: /var/lib/pvm-exit", "state_dir: state", 1),
		"two docs":    good + "---\n" + good,
		"empty":       "",
	} {
		if _, err := ParseConfig([]byte(doc)); err == nil {
			t.Errorf("%s: ParseConfig must fail", name)
		}
	}
}

func TestUnits(t *testing.T) {
	kernel := SystemUnit("/usr/local/bin/pvm-exit", "/etc/pvm-exit/config.yaml", exit.NATAuto)
	for _, want := range []string{"User=pvm-exit", "AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW", "NoNewPrivileges=yes", "ExecStart=/usr/local/bin/pvm-exit serve --config /etc/pvm-exit/config.yaml", "WantedBy=multi-user.target"} {
		if !strings.Contains(kernel, want) {
			t.Errorf("system unit missing %q:\n%s", want, kernel)
		}
	}
	if us := SystemUnit("/usr/bin/pvm-exit", "/etc/pvm-exit/config.yaml", exit.NATUserspace); !strings.Contains(us, "AmbientCapabilities=\n") || strings.Contains(us, "CAP_NET") {
		t.Errorf("userspace NAT must run without capabilities:\n%s", us)
	}
	user := UserUnit("/home/pvm/bin/pvm exit", "/home/pvm/.config/pvm-exit/config.yaml")
	if !strings.Contains(user, `ExecStart="/home/pvm/bin/pvm exit" serve`) || strings.Contains(user, "User=") || !strings.Contains(user, "WantedBy=default.target") {
		t.Errorf("user unit:\n%s", user)
	}
}

// The unit `pvm exit-agent install` writes migrates with its state and
// token in place.
func TestConfigFromLegacyUnit(t *testing.T) {
	d, _ := DirsFor(ModeSystem)
	unit := "[Unit]\nDescription=pvm exit agent\n\n[Service]\nType=simple\nExecStart=/usr/local/bin/pvm exit-agent --listen :18443 --token-file /etc/pvm-exit/token --state /var/lib/pvm-exit --transport both --nat auto --tun-prefix pvmexit --subnet-base 99 --udp-port 18443 --tcp-port 18444 --public-endpoint vps.example.com --timezone Asia/Tokyo --domain-allow-cidr 10.0.0.0/8,192.168.0.0/16\nRestart=always\n"
	c, err := configFromLegacyUnit(unit, DefaultConfig(d))
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminTokenFile != "/etc/pvm-exit/token" || c.StateDir != "/var/lib/pvm-exit" || c.PublicEndpoint != "vps.example.com" || c.Timezone != "Asia/Tokyo" || len(c.DomainAllow) != 2 {
		t.Fatalf("migrated config = %+v", c)
	}
	for name, bad := range map[string]string{
		"inline token": strings.Replace(unit, "--token-file /etc/pvm-exit/token", "--token YOUR_TOKEN", 1),
		"no token":     strings.Replace(unit, "--token-file /etc/pvm-exit/token ", "", 1),
		"unknown flag": strings.Replace(unit, "--nat auto", "--frobnicate 1", 1),
		"no agent":     "[Service]\nExecStart=/bin/true\n",
	} {
		if _, err := configFromLegacyUnit(bad, DefaultConfig(d)); err == nil {
			t.Errorf("%s: migration must fail", name)
		}
	}
}

// A migrated token in the config directory takes the standard name through
// a second link; the legacy forwarding file goes only when pvm wrote it.
func TestMigrateTidiesLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	d := Dirs{Config: dir, State: t.TempDir()}
	std := DefaultConfig(d).AdminTokenFile
	legacy := filepath.Join(dir, "token")
	if got := standardTokenPath(d, legacy); got != std {
		t.Fatalf("standardTokenPath = %q, want %q", got, std)
	}
	for _, keep := range []string{std, "/root/pvm-token"} {
		if got := standardTokenPath(d, keep); got != "" {
			t.Errorf("standardTokenPath(%q) = %q, want it kept in place", keep, got)
		}
	}
	if err := os.WriteFile(legacy, []byte("YOUR_TOKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := linkToken(legacy, std); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(std); err != nil || string(b) != "YOUR_TOKEN\n" {
		t.Fatalf("standard token = %q, %v", b, err)
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linkToken(other, std); err == nil {
		t.Fatal("a different file at the standard name must not be replaced")
	}

	ours, edited := filepath.Join(dir, "99-ours.conf"), filepath.Join(dir, "99-edited.conf")
	if err := os.WriteFile(ours, []byte("net.ipv4.ip_forward=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, []byte("net.ipv4.ip_forward=1\nnet.core.somaxconn=4096\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ours, edited, filepath.Join(dir, "missing.conf")} {
		if err := removeLegacySysctl(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Error("the file pvm wrote must be removed")
	}
	if _, err := os.Stat(edited); err != nil {
		t.Error("an edited file must stay")
	}
}

func TestInviteServer(t *testing.T) {
	c := Config{Listen: ":18443"}
	if _, err := inviteServer("", c); err == nil {
		t.Fatal("an invite needs a public address")
	}
	c.PublicEndpoint = "vps.example.com:443"
	if got, _ := inviteServer("", c); got != "vps.example.com:18443" {
		t.Fatalf("invite server = %q", got)
	}
	if got, _ := inviteServer("203.0.113.7:8443", c); got != "203.0.113.7:8443" {
		t.Fatalf("--server must win, got %q", got)
	}
}

func TestAdminTokenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "admin.token")
	created, err := ensureAdminToken(p)
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	first, err := ReadAdminToken(p)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := ensureAdminToken(p); err != nil || created {
		t.Fatalf("an existing token must be kept: %v, %v", created, err)
	}
	if again, _ := ReadAdminToken(p); again != first {
		t.Fatal("the admin token changed")
	}
	if err := os.Chmod(p, 0o644); err == nil {
		if fi, _ := os.Stat(p); fi.Mode().Perm()&0o077 != 0 {
			if _, err := ReadAdminToken(p); err == nil {
				t.Fatal("a group- or world-readable token must be refused")
			}
		}
	}
}

func TestPingRangeAllows(t *testing.T) {
	for s, want := range map[string]bool{"0\t2147483647\n": true, "1\t0\n": false, "100 200": true, "": false} {
		if got := pingRangeAllows(s, 150); got != want {
			t.Errorf("pingRangeAllows(%q, 150) = %v, want %v", s, got, want)
		}
	}
}
