package exitsvc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/huangzheng2016/pvm/internal/exit"
	"github.com/huangzheng2016/pvm/internal/fsutil"
)

const usage = `usage: pvm-exit <command> [args]

  init [--user] [--migrate]   install and start the service (system: as root; --user: no root)
  serve [--config FILE]       run the server in the foreground (what the service runs)
  invite <name>               create a one-time invite for a client host
  invite ls | invite rm <id>  list or withdraw pending invites
  token ls | token rm <id>    list or revoke issued tokens (rm also removes their peers)
  peers                       list registered peers
  info [--json]               show config, fingerprint and service state
  unit [--exe PATH]           print the system unit (used to build the package)
  uninstall [--user]          stop and remove the service; config and state are kept
  version
`

// Main runs pvm-exit; args excludes the program name.
func Main(args []string, version string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "invite":
		return cmdInvite(args[1:])
	case "token":
		return cmdToken(args[1:])
	case "peers":
		return cmdPeers(args[1:])
	case "info":
		return cmdInfo(args[1:])
	case "unit":
		return cmdUnit(args[1:])
	case "uninstall":
		return cmdUninstall(args[1:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "pvm-exit: unknown command %q\n\n%s", args[0], usage)
	return 2
}

func fail(format string, a ...any) int {
	fmt.Fprintf(os.Stderr, "pvm-exit: "+format+"\n", a...)
	return 1
}

func newFlags(name, args string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: pvm-exit %s %s\n", name, args)
		fs.PrintDefaults()
	}
	return fs
}

// parseArgs accepts positionals before or after the flags.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		pos = append(pos, args[0])
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return append(pos, fs.Args()...), nil
}

// defaultConfigPath prefers the invoking user's install, then the system
// one.
func defaultConfigPath() string {
	if os.Geteuid() != 0 {
		if d, err := DirsFor(ModeUser); err == nil {
			if _, err := os.Stat(d.ConfigPath()); err == nil {
				return d.ConfigPath()
			}
		}
	}
	d, _ := DirsFor(ModeSystem)
	return d.ConfigPath()
}

func cmdServe(args []string) int {
	fs := newFlags("serve", "[--config FILE]")
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	verbose := fs.Bool("v", false, "verbose wireguard logging")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) > 0 {
		return 2
	}
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	tok, err := ReadAdminToken(cfg.AdminTokenFile)
	if err != nil {
		return fail("%v", err)
	}
	sc := cfg.ServerConfig(tok)
	sc.Verbose = *verbose
	srv, err := exit.NewServer(sc)
	if err != nil {
		return fail("%v", err)
	}
	fp, err := srv.Start()
	if err != nil {
		return fail("%v", err)
	}
	fmt.Printf("pvm-exit listening %s (nat %s)\n", cfg.Listen, cfg.NAT)
	fmt.Printf("cert fingerprint: %s\n", fp)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	fmt.Println("shutting down")
	srv.Close()
	return 0
}

// userBus lets `systemctl --user` reach the user manager from sessions
// that did not set XDG_RUNTIME_DIR, such as `ssh host cmd` or su.
func userBus() error {
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return nil
	}
	dir := fmt.Sprintf("/run/user/%d", os.Getuid())
	if _, err := os.Stat(filepath.Join(dir, "bus")); err != nil {
		return fmt.Errorf("no systemd user session (%s/bus is missing): log in over ssh or a console session, or run `sudo loginctl enable-linger %s`, then retry", dir, currentUser())
	}
	return os.Setenv("XDG_RUNTIME_DIR", dir)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func newAdminToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ensureAdminToken creates the token file once and never overwrites it.
func ensureAdminToken(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		_, err := ReadAdminToken(path)
		return false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	tok, err := newAdminToken()
	if err != nil {
		return false, err
	}
	return true, fsutil.AtomicWrite(path, []byte(tok+"\n"), 0o600)
}

func cmdInit(args []string) int {
	fs := newFlags("init", "[--user] [--migrate] [--public-endpoint HOST] [--listen :18443] [--transport both|udp|tcp] [--nat auto|nft|iptables|userspace] [--no-start]")
	userMode := fs.Bool("user", false, "install a systemd user service for this user: no root, userspace NAT")
	migrate := fs.Bool("migrate", false, "take over an existing `pvm exit-agent` service: its token, certificate and peers")
	pub := fs.String("public-endpoint", "", "host clients reach this server at (also goes into invites)")
	listen := fs.String("listen", "", "control plane address (default :18443)")
	transport := fs.String("transport", "", "both | udp | tcp (default both)")
	nat := fs.String("nat", "", "auto | nft | iptables | userspace (user mode: userspace only)")
	noStart := fs.Bool("no-start", false, "write everything but do not enable or start the service")
	packaged := fs.Bool("packaged", false, "for the .deb maintainer script: use the unit the package installed")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) > 0 {
		return 2
	}
	if runtime.GOOS != "linux" {
		return fail("init installs a systemd service and runs on Linux only; use `pvm-exit serve` elsewhere")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fail("systemctl not found: init needs systemd; run `pvm-exit serve --config FILE` under your own supervisor")
	}
	mode := ModeSystem
	if *userMode {
		mode = ModeUser
	}
	d, err := DirsFor(mode)
	if err != nil {
		return fail("%v", err)
	}
	if mode == ModeSystem && os.Geteuid() != 0 {
		return fail("system install needs root (sudo pvm-exit init); without root use `pvm-exit init --user`")
	}
	if mode == ModeUser && os.Geteuid() == 0 {
		return fail("--user installs for a regular user; as root use `pvm-exit init`")
	}
	if mode == ModeUser {
		if err := userBus(); err != nil {
			return fail("%v", err)
		}
	}
	if *packaged && mode == ModeUser {
		return fail("--packaged is for the system package")
	}
	if *packaged {
		if _, err := os.Stat(PackagedUnitPath); err != nil {
			return fail("--packaged: %s is missing; reinstall the package", PackagedUnitPath)
		}
	}
	if *migrate && mode == ModeUser {
		return fail("--migrate takes over the root `pvm exit-agent` service; run it without --user as root")
	}

	cfg := DefaultConfig(d)
	existing := false
	movedToken := ""
	if _, err := os.Stat(d.ConfigPath()); err == nil {
		if *migrate {
			return fail("%s already exists; --migrate only creates a new config", d.ConfigPath())
		}
		if cfg, err = LoadConfig(d.ConfigPath()); err != nil {
			return fail("%v", err)
		}
		existing = true
	} else if *migrate {
		if cfg, err = migratedConfig(d); err != nil {
			return fail("%v", err)
		}
		if std := standardTokenPath(d, cfg.AdminTokenFile); std != "" {
			movedToken, cfg.AdminTokenFile = cfg.AdminTokenFile, std
		}
	} else if _, err := os.Stat(legacyUnitPath); err == nil && mode == ModeSystem {
		rerun := "sudo pvm-exit init --migrate"
		if *packaged {
			rerun = "sudo pvm-exit init --packaged --migrate"
		}
		return fail("a `pvm exit-agent` service is installed here (%s); run `%s` to take over its token, certificate and peers", legacyUnitPath, rerun)
	}
	for _, o := range []struct {
		flag string
		dst  *string
	}{{*pub, &cfg.PublicEndpoint}, {*listen, &cfg.Listen}, {*transport, &cfg.Transport}, {*nat, &cfg.NAT}} {
		if o.flag != "" {
			if existing && *o.dst != o.flag {
				return fail("%s exists; edit it instead of passing flags that change it", d.ConfigPath())
			}
			*o.dst = o.flag
		}
	}
	if mode == ModeUser {
		if cfg.NAT != exit.NATUserspace {
			return fail("user mode supports only nat: userspace (kernel NAT needs root)")
		}
		for _, p := range []int{atoiOr(cfg.ListenPort()), cfg.UDPPort, cfg.TCPPort} {
			if p < 1024 {
				return fail("user mode cannot bind port %d (below 1024)", p)
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return fail("%v", err)
	}

	var uid, gid = -1, -1
	if mode == ModeSystem {
		if uid, gid, err = ensureServiceUser(cfg.StateDir); err != nil {
			return fail("%v", err)
		}
		if err := os.MkdirAll(d.Config, 0o750); err != nil {
			return fail("%v", err)
		}
		if err := os.Chown(d.Config, 0, gid); err != nil {
			return fail("%v", err)
		}
		if err := os.Chmod(d.Config, 0o750); err != nil {
			return fail("%v", err)
		}
	} else if err := os.MkdirAll(d.Config, 0o700); err != nil {
		return fail("%v", err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fail("%v", err)
	}
	if movedToken != "" {
		if err := linkToken(movedToken, cfg.AdminTokenFile); err != nil {
			return fail("%v", err)
		}
	}
	created, err := ensureAdminToken(cfg.AdminTokenFile)
	if err != nil {
		return fail("%v", err)
	}
	if !existing {
		perm := os.FileMode(0o600)
		if mode == ModeSystem {
			perm = 0o640
		}
		if err := fsutil.AtomicWrite(d.ConfigPath(), cfg.Marshal(), perm); err != nil {
			return fail("write config: %v", err)
		}
		if mode == ModeSystem {
			if err := os.Chown(d.ConfigPath(), 0, gid); err != nil {
				return fail("%v", err)
			}
		}
	}
	_, fp, err := exit.EnsureCert(cfg.StateDir)
	if err != nil {
		return fail("%v", err)
	}
	if mode == ModeSystem {
		if err := os.Chown(cfg.AdminTokenFile, uid, gid); err != nil {
			return fail("%v", err)
		}
		if err := chownTree(cfg.StateDir, uid, gid); err != nil {
			return fail("%v", err)
		}
		if cfg.NAT != exit.NATUserspace {
			if err := fsutil.AtomicWrite(SysctlPath, []byte(SysctlContent), 0o644); err != nil {
				return fail("write %s: %v", SysctlPath, err)
			}
			if err := run("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
				return fail("%v", err)
			}
			if err := removeLegacySysctl(legacySysctlPath); err != nil {
				return fail("%v", err)
			}
		}
	}

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fail("%v", err)
	}
	unitPath, unit, sctl := systemUnitPath, SystemUnit(exe, d.ConfigPath(), cfg.NAT), []string{}
	if mode == ModeUser {
		cfgHome := filepath.Dir(d.Config)
		unitPath, unit, sctl = filepath.Join(cfgHome, "systemd", "user", unitName), UserUnit(exe, d.ConfigPath()), []string{"--user"}
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			return fail("%v", err)
		}
	}
	if *packaged {
		// the package ships the unit; one an earlier `init` wrote to /etc
		// would shadow it
		unitPath = PackagedUnitPath
		if _, err := os.Stat(systemUnitPath); err == nil {
			if err := os.Rename(systemUnitPath, systemUnitPath+".bak"); err != nil {
				return fail("%v", err)
			}
			fmt.Printf("moved %s to %s.bak; the package's %s is used now\n", systemUnitPath, systemUnitPath, PackagedUnitPath)
		}
		if cfg.NAT == exit.NATUserspace {
			if err := os.MkdirAll(filepath.Dir(PackagedNATDropIn), 0o755); err != nil {
				return fail("%v", err)
			}
			if err := fsutil.AtomicWrite(PackagedNATDropIn, []byte(PackagedNATDropInContent), 0o644); err != nil {
				return fail("write %s: %v", PackagedNATDropIn, err)
			}
		} else if err := os.Remove(PackagedNATDropIn); err != nil && !os.IsNotExist(err) {
			return fail("%v", err)
		}
	} else if err := fsutil.AtomicWrite(unitPath, []byte(unit), 0o644); err != nil {
		return fail("write unit: %v", err)
	}
	if *migrate {
		if err := retireLegacyUnit(); err != nil {
			return fail("%v", err)
		}
		if movedToken != "" {
			if err := os.Remove(movedToken); err != nil {
				return fail("%v", err)
			}
		}
	}
	if err := run("systemctl", append(sctl, "daemon-reload")...); err != nil {
		return fail("%v", err)
	}
	if !*noStart {
		if err := run("systemctl", append(sctl, "enable", unitName)...); err != nil {
			return fail("%v", err)
		}
		if err := run("systemctl", append(sctl, "restart", unitName)...); err != nil {
			return fail("%v; inspect `journalctl %s-u %s -n 30 --no-pager`", err, userFlag(mode), unitName)
		}
		if err := waitListening(cfg.ListenPort(), 20*time.Second); err != nil {
			return fail("the service started but %v; inspect `journalctl %s-u %s -n 30 --no-pager`", err, userFlag(mode), unitName)
		}
	}

	fmt.Printf("pvm-exit %s install ready\n", mode)
	fmt.Printf("  config:      %s%s\n", d.ConfigPath(), map[bool]string{true: " (kept)", false: ""}[existing])
	fmt.Printf("  state:       %s\n", cfg.StateDir)
	tokenNote := map[bool]string{true: " (new)", false: " (kept)"}[created]
	if movedToken != "" {
		tokenNote = " (kept, moved from " + movedToken + ")"
	}
	fmt.Printf("  admin token: %s%s\n", cfg.AdminTokenFile, tokenNote)
	fmt.Printf("  fingerprint: %s\n", fp)
	fmt.Printf("  service:     %s (%s)\n", unitPath, map[bool]string{true: "not started", false: "started"}[*noStart])
	fmt.Printf("open TCP %s and UDP %d (WireGuard/UDP), TCP %d (WireGuard/TCP) in the VPS firewall\n", cfg.ListenPort(), cfg.UDPPort, cfg.TCPPort)
	if effective, err := exit.ResolveNATName(cfg.NAT); err == nil && effective == exit.NATUserspace {
		g := os.Getgid()
		if mode == ModeSystem {
			g = gid
		}
		if !pingGroupAllows(g) {
			fmt.Printf("ICMP from clients cannot leave this exit: unprivileged ping sockets are off for group %d. As root: echo 'net.ipv4.ping_group_range = 0 2147483647' > /etc/sysctl.d/60-ping-group.conf && sysctl --system\n", g)
		}
	}
	if mode == ModeUser {
		if out, err := exec.Command("loginctl", "show-user", currentUser(), "-p", "Linger", "--value").Output(); err != nil || strings.TrimSpace(string(out)) != "yes" {
			fmt.Printf("the service stops when you log out unless lingering is on: loginctl enable-linger %s (may need an administrator)\n", currentUser())
		}
	}
	if *migrate {
		fmt.Println("migrated: existing exits keep working with the same token and fingerprint")
	}
	fmt.Printf("next: %spvm-exit invite <client-name>\n", map[bool]string{true: "sudo ", false: ""}[mode == ModeSystem])
	return 0
}

func userFlag(mode Mode) string {
	if mode == ModeUser {
		return "--user "
	}
	return ""
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

const systemUnitPath = "/etc/systemd/system/" + unitName

// ensureServiceUser creates the pvm-exit system user when missing.
func ensureServiceUser(home string) (int, int, error) {
	u, err := user.Lookup(ServiceUser)
	if err != nil {
		if err := run("useradd", "--system", "--user-group", "--home-dir", home, "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser); err != nil {
			return -1, -1, err
		}
		if u, err = user.Lookup(ServiceUser); err != nil {
			return -1, -1, err
		}
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return -1, -1, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return -1, -1, err
	}
	return uid, gid, nil
}

func chownTree(root string, uid, gid int) error {
	return filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// adminClient reaches the local server with the admin token, pinned to the
// certificate in the state directory.
func adminClient(cfgPath string) (*exit.Client, Config, error) {
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Config{}, fmt.Errorf("%s not found: run `pvm-exit init` first, or pass --config", cfgPath)
		}
		return nil, Config{}, err
	}
	tok, err := ReadAdminToken(cfg.AdminTokenFile)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, Config{}, fmt.Errorf("%v (system installs need sudo)", err)
		}
		return nil, Config{}, err
	}
	fp, err := exit.StateFingerprint(cfg.StateDir)
	if err != nil {
		return nil, Config{}, fmt.Errorf("read the server certificate: %w", err)
	}
	host, port, _ := net.SplitHostPort(cfg.Listen)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return exit.NewClient(net.JoinHostPort(host, port), tok, fp), cfg, nil
}

func ctx30() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// inviteServer is where clients reach the control plane: --server, else
// public_endpoint's host with the listen port.
func inviteServer(flagServer string, cfg Config) (string, error) {
	if flagServer != "" {
		if _, _, err := net.SplitHostPort(flagServer); err != nil {
			return "", fmt.Errorf("--server %q is not host:port", flagServer)
		}
		return flagServer, nil
	}
	if cfg.PublicEndpoint == "" {
		return "", errors.New("clients need this server's public address: pass --server vps.example.com:18443 or set public_endpoint in the config")
	}
	host := cfg.PublicEndpoint
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.JoinHostPort(host, cfg.ListenPort()), nil
}

func cmdInvite(args []string) int {
	if len(args) > 0 && (args[0] == "ls" || args[0] == "rm") {
		return cmdInviteAdmin(args[0], args[1:])
	}
	fs := newFlags("invite", "<client-name> [--role enroll|admin] [--ttl 15m] [--max-peers 64] [--server host:port] [--exit my-exit] [--raw]")
	role := fs.String("role", exit.RoleEnroll, "enroll registers and manages only its own peers; admin manages everything")
	ttl := fs.Duration("ttl", exit.DefaultInviteTTL, "how long the invite stays valid (at most 24h)")
	maxPeers := fs.Int("max-peers", exit.DefaultMaxPeers, "peers the issued enroll token may hold (0 = no limit)")
	server := fs.String("server", "", "host:port clients use for this server (default: public_endpoint and the listen port)")
	hostOnly := fs.String("host", "", "host clients use for this server, with the configured listen port")
	exitName := fs.String("exit", "my-exit", "exit name shown in the suggested `pvm exit add` command")
	raw := fs.Bool("raw", false, "print only the invite string")
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	name := pos[0]
	if !exit.ValidTokenName(name) {
		return fail("invalid client name %q (1-64 letters, digits, '.', '_' or '-')", name)
	}
	c, cfg, err := adminClient(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	if *hostOnly != "" {
		if *server != "" {
			return fail("pass --server or --host, not both")
		}
		*server = net.JoinHostPort(*hostOnly, cfg.ListenPort())
	}
	addr, err := inviteServer(*server, cfg)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := ctx30()
	defer cancel()
	req := exit.CreateInviteRequest{Name: name, Role: *role, TTLSeconds: int(ttl.Seconds()), MaxPeers: maxPeers}
	created, err := c.CreateInvite(ctx, req)
	if err != nil {
		return fail("create invite: %v (is the service running? `pvm-exit info`)", err)
	}
	inv := exit.Invite{Server: addr, Fingerprint: c.Fingerprint, Secret: created.Secret}.String()
	if *raw {
		fmt.Println(inv)
		return 0
	}
	limit := "no peer limit"
	if created.MaxPeers > 0 {
		limit = fmt.Sprintf("up to %d peers", created.MaxPeers)
	}
	fmt.Fprintf(os.Stderr, "invite %s for %q: role %s, %s, valid until %s, usable once\n", created.ID, name, created.Role, limit, created.ExpiresAt.Local().Format(time.RFC3339))
	fmt.Fprintln(os.Stderr, "it carries a secret: pass it only over a channel you trust (this ssh session); on the client host run:")
	fmt.Printf("pvm exit add %s --invite %s\n", *exitName, inv)
	return 0
}

func cmdInviteAdmin(sub string, args []string) int {
	fs := newFlags("invite "+sub, map[string]string{"ls": "", "rm": "<id>"}[sub])
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	pos, err := parseArgs(fs, args)
	if err != nil || (sub == "ls" && len(pos) != 0) || (sub == "rm" && len(pos) != 1) {
		return 2
	}
	c, _, err := adminClient(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := ctx30()
	defer cancel()
	if sub == "rm" {
		if err := c.DeleteInvite(ctx, pos[0]); err != nil {
			return fail("%v", err)
		}
		fmt.Printf("invite %s withdrawn\n", pos[0])
		return 0
	}
	invs, err := c.ListInvites(ctx)
	if err != nil {
		return fail("%v", err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tROLE\tMAX PEERS\tEXPIRES")
	for _, i := range invs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", i.ID, i.Name, i.Role, maxPeersText(i.MaxPeers), i.ExpiresAt.Local().Format(time.RFC3339))
	}
	return flushTable(tw)
}

func maxPeersText(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func flushTable(tw *tabwriter.Writer) int {
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	return 0
}

func cmdToken(args []string) int {
	if len(args) == 0 || (args[0] != "ls" && args[0] != "rm") {
		fmt.Fprintln(os.Stderr, "usage: pvm-exit token ls | token rm <id> [--keep-peers]")
		return 2
	}
	sub := args[0]
	fs := newFlags("token "+sub, map[string]string{"ls": "", "rm": "<id> [--keep-peers]"}[sub])
	keep := fs.Bool("keep-peers", false, "revoke the token but leave its peers connected until they idle out")
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || (sub == "ls" && len(pos) != 0) || (sub == "rm" && len(pos) != 1) {
		return 2
	}
	c, _, err := adminClient(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := ctx30()
	defer cancel()
	if sub == "rm" {
		if pos[0] == "admin" {
			return fail("the admin token comes from admin_token_file and cannot be revoked here: write a new token into that file and restart the service")
		}
		n, err := c.DeleteToken(ctx, pos[0], *keep)
		if err != nil {
			return fail("%v", err)
		}
		fmt.Printf("token %s revoked; %d peers removed\n", pos[0], n)
		return 0
	}
	toks, err := c.ListTokens(ctx)
	if err != nil {
		return fail("%v", err)
	}
	peers, err := c.ListPeers(ctx)
	if err != nil {
		return fail("%v", err)
	}
	held := map[string]int{}
	for _, p := range peers {
		held[p.Owner]++
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tROLE\tPEERS\tMAX PEERS\tCREATED")
	fmt.Fprintf(tw, "admin\t(admin token file)\tadmin\t%d\t-\t-\n", held[""])
	for _, t := range toks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", t.ID, t.Name, t.Role, held[t.ID], maxPeersText(t.MaxPeers), t.CreatedAt.Local().Format(time.RFC3339))
	}
	return flushTable(tw)
}

func cmdPeers(args []string) int {
	fs := newFlags("peers", "")
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		return 2
	}
	c, _, err := adminClient(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := ctx30()
	defer cancel()
	peers, err := c.ListPeers(ctx)
	if err != nil {
		return fail("%v", err)
	}
	toks, err := c.ListTokens(ctx)
	if err != nil {
		return fail("%v", err)
	}
	names := map[string]string{"": "admin"}
	for _, t := range toks {
		names[t.ID] = t.Name + " (" + t.ID + ")"
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TUNNEL IP\tTRANSPORT\tHOSTNAME\tTOKEN\tCREATED")
	for _, p := range peers {
		owner, ok := names[p.Owner]
		if !ok {
			owner = "revoked (" + p.Owner + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.IP, p.Transport, p.Hostname, owner, p.CreatedAt.Local().Format(time.RFC3339))
	}
	return flushTable(tw)
}

func cmdInfo(args []string) int {
	fs := newFlags("info", "[--config FILE] [--json]")
	cfgPath := fs.String("config", defaultConfigPath(), "config file")
	asJSON := fs.Bool("json", false, "print one JSON object")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		return 2
	}
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	fp, err := exit.StateFingerprint(cfg.StateDir)
	if err != nil {
		fp = "unknown (" + err.Error() + ")"
	}
	mode := ModeSystem
	if u, err := DirsFor(ModeUser); err == nil && *cfgPath == u.ConfigPath() {
		mode = ModeUser
	}
	state := "unknown"
	if out, err := exec.Command("systemctl", append(strings.Fields(userFlag(mode)), "is-active", unitName)...).Output(); err == nil || len(out) > 0 {
		state = strings.TrimSpace(string(out))
	}
	if *asJSON {
		b, _ := json.Marshal(Info{Mode: mode, Config: *cfgPath, StateDir: cfg.StateDir, Listen: cfg.Listen, ListenPort: cfg.ListenPort(), PublicEndpoint: cfg.PublicEndpoint, NAT: cfg.NAT, Transport: cfg.Transport, Fingerprint: fp, Service: state})
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("mode:            %s\nconfig:          %s\nstate:           %s\nlisten:          %s\n", mode, *cfgPath, cfg.StateDir, cfg.Listen)
	fmt.Printf("public endpoint: %s\nnat:             %s\ntransport:       %s (udp %d, tcp %d)\n", orDash(cfg.PublicEndpoint), cfg.NAT, cfg.Transport, cfg.UDPPort, cfg.TCPPort)
	fmt.Printf("fingerprint:     %s\nservice:         %s\n", fp, state)
	return 0
}

// Info is `pvm-exit info --json`, which `pvm exit deploy` reads.
type Info struct {
	Mode           Mode   `json:"mode"`
	Config         string `json:"config"`
	StateDir       string `json:"state_dir"`
	Listen         string `json:"listen"`
	ListenPort     string `json:"listen_port"`
	PublicEndpoint string `json:"public_endpoint"`
	NAT            string `json:"nat"`
	Transport      string `json:"transport"`
	Fingerprint    string `json:"fingerprint"`
	Service        string `json:"service"`
}

func cmdUnit(args []string) int {
	fs := newFlags("unit", "[--exe PATH] [--nat auto|nft|iptables|userspace]")
	exe := fs.String("exe", "/usr/bin/pvm-exit", "binary path in ExecStart")
	nat := fs.String("nat", exit.NATAuto, "nat backend the unit grants capabilities for")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		return 2
	}
	d, _ := DirsFor(ModeSystem)
	fmt.Print(SystemUnit(*exe, d.ConfigPath(), *nat))
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdUninstall(args []string) int {
	fs := newFlags("uninstall", "[--user]")
	userMode := fs.Bool("user", false, "remove the user service")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		return 2
	}
	mode := ModeSystem
	if *userMode {
		mode = ModeUser
	}
	d, err := DirsFor(mode)
	if err != nil {
		return fail("%v", err)
	}
	unitPath, sctl := systemUnitPath, []string{}
	if mode == ModeUser {
		if err := userBus(); err != nil {
			return fail("%v", err)
		}
		unitPath, sctl = filepath.Join(filepath.Dir(d.Config), "systemd", "user", unitName), []string{"--user"}
	} else if os.Geteuid() != 0 {
		return fail("removing the system service needs root (sudo pvm-exit uninstall)")
	}
	if _, err := os.Stat(unitPath); err != nil {
		return fail("%s not found: nothing to uninstall", unitPath)
	}
	if err := run("systemctl", append(sctl, "disable", "--now", unitName)...); err != nil {
		return fail("%v", err)
	}
	if err := os.Remove(unitPath); err != nil {
		return fail("%v", err)
	}
	if err := run("systemctl", append(sctl, "daemon-reload")...); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("service removed; config %s and state %s were kept (they hold the admin token, certificate and peers)\n", d.Config, d.State)
	return 0
}

// waitListening waits for the control port to accept connections, so
// "ready" and the next `pvm-exit invite` do not race the service start.
func waitListening(port string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("nothing listens on port %s after %s", port, limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
