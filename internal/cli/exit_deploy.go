package cli

import (
	"bufio"
	"bytes"
	"debug/elf"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/exit"
)

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, " ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

var releaseTagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?$`)

const inviteMarker = "PVM-INVITE "

// deployProbe prints OS, architecture, uid and user name, then
// "pvm-exit=<version> <path>" when pvm-exit is there and "sudo=<how>". It
// succeeds on a VPS without pvm-exit or sudo.
const deployProbe = `uname -s; uname -m; id -u; id -un
if [ -f /usr/lib/systemd/system/pvm-exit.service ]; then p=/usr/bin/pvm-exit; else p=$(command -v pvm-exit || echo "$HOME/.local/bin/pvm-exit"); fi
if v=$("$p" version 2>/dev/null); then echo "pvm-exit=$v $p"; fi
echo "linger=$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)"
if [ "$(id -u)" = 0 ]; then echo sudo=root; elif ! command -v sudo >/dev/null 2>&1; then echo sudo=none; elif sudo -n true 2>/dev/null; then echo sudo=nopass; else echo sudo=pass; fi
`

// deployBootstrap runs the whole deploy in one ssh session, so ssh and sudo
// each ask for a password at most once. After the probe it reads from
// stdin, line by line: "go" (anything else stops), how to run root
// commands, the sudo password for "sudo-pw" (checked, up to three tries),
// the base64 setup script, and "bin" followed by the pvm-exit binary up to
// EOF, or "none". S runs a command as root; with a password it is fed to
// sudo on stdin, so it is in no argument, environment or file, and the
// command itself gets /dev/null as stdin.
const deployBootstrap = "set -e\numask 077\n" + deployProbe + `echo PVM-PROBED
IFS= read -r step || exit 0
[ "$step" = go ] || exit 0
IFS= read -r how
pw=
case "$how" in
sudo-pw)
  n=0
  while :; do
    IFS= read -r pw || exit 1
    if e=$(printf '%s\n' "$pw" | sudo -S -p '' -v 2>&1); then echo PVM-SUDO-OK; break; fi
    case "$e" in *tty*|*terminal*) echo PVM-SUDO-TTY; exit 1 ;; esac
    n=$((n+1))
    echo PVM-SUDO-BAD
    [ "$n" -lt 3 ] || exit 1
  done
  S() { printf '%s\n' "$pw" | sudo -S -p '' sh -c 'exec "$@" </dev/null' pvm-sudo "$@"; } ;;
sudo) S() { sudo -n "$@"; } ;;
*) S() { "$@"; } ;;
esac
d=$(mktemp -d)
trap 'rm -rf "$d"' EXIT
IFS= read -r script
printf '%s' "$script" | base64 -d > "$d/setup.sh"
IFS= read -r what
if [ "$what" = bin ]; then cat > "$d/pvm-exit"; chmod 700 "$d/pvm-exit"; fi
exec </dev/null
. "$d/setup.sh"
`

// deployTarget is what the probe of the VPS learned.
type deployTarget struct {
	arch      string // amd64 | arm64
	root      bool
	user      string
	sudo      string // root | none | nopass | pass
	installed string // version of the pvm-exit already there, if any
	path      string // where that pvm-exit is
	packaged  bool   // that pvm-exit came from the .deb
	linger    bool   // the user's services keep running after logout
}

// releaseWanted is the pvm-exit release to install: --tag, else this
// pvm's own version when it is a release; "" when there is none.
func releaseWanted(tag string) string {
	if tag != "" {
		return tag
	}
	if releaseTagRE.MatchString(buildinfo.Version) {
		return buildinfo.Version
	}
	return ""
}

// exitDeploy installs pvm-exit on a VPS over ssh, starts it, and adds the
// exit here with a one-time invite. ssh authenticates the server, so no
// fingerprint has to be copied by hand.
func exitDeploy(args []string) int {
	fs := newFlagSet("exit deploy", "<name> --ssh [user@]host [--ssh-opt OPT]... [--user-mode] [--public-endpoint HOST] [--migrate] [--binary FILE | --tag vX.Y.Z] [--client NAME] [--transport auto|udp|tcp] [--tz TZ] [--locale L]")
	target := fs.String("ssh", "", "ssh destination of the VPS, as for ssh(1)")
	var sshOpts stringList
	fs.Var(&sshOpts, "ssh-opt", "extra ssh argument, repeatable (e.g. --ssh-opt=-p --ssh-opt=2222)")
	userMode := fs.Bool("user-mode", false, "install as a systemd user service of the ssh user (no root, userspace NAT)")
	pub := fs.String("public-endpoint", "", "host clients use for the exit (default: the ssh host)")
	migrate := fs.Bool("migrate", false, "take over an existing `pvm exit-agent` service on the VPS")
	binary := fs.String("binary", "", "local linux pvm-exit to upload instead of the release download")
	tag := fs.String("tag", "", "release to install (default: this pvm's version)")
	client := fs.String("client", "", "name of this host's token on the exit (default: hostname)")
	transport := fs.String("transport", "auto", "auto | udp | tcp")
	tz := fs.String("tz", "", "IANA timezone of this exit's region")
	locale := fs.String("locale", "", "locale matching this exit's region")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if name == "" || *target == "" || fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	if *binary != "" && *tag != "" {
		return fatalf("pass --binary or --tag, not both")
	}
	dir, err := exitsDir()
	if err != nil {
		return fatalf("%v", err)
	}
	if _, err := exit.LoadEntry(dir, name); err == nil {
		return fatalf("exit %q already exists", name)
	}
	host := *pub
	if host == "" {
		host = sshHost(*target)
	}
	if strings.ContainsAny(host, " \t'\"\\$`;&|<>") || host == "" {
		return fatalf("invalid public endpoint %q", host)
	}
	clientName := *client
	if clientName == "" {
		clientName = defaultClientName()
	}
	if !exit.ValidTokenName(clientName) {
		return fatalf("invalid --client %q (1-64 letters, digits, '.', '_' or '-')", clientName)
	}

	fmt.Printf("connecting to %s...\n", *target)
	sess := exec.Command("ssh", append(append([]string{}, sshOpts...), "-T", *target, "sh -c "+shellQuoteArg(deployBootstrap))...)
	sess.Stderr = os.Stderr
	stdin, err := sess.StdinPipe()
	if err != nil {
		return fatalf("%v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return fatalf("%v", err)
	}
	if err := sess.Start(); err != nil {
		return fatalf("ssh: %v", err)
	}
	out := bufio.NewReader(stdout)
	// stop ends the session before anything was changed on the VPS.
	stop := func(format string, a ...any) int {
		fmt.Fprintln(stdin, "stop")
		stdin.Close()
		_, _ = io.Copy(io.Discard, out)
		_ = sess.Wait()
		return fatalf(format, a...)
	}
	readLine := func() (string, error) {
		line, err := out.ReadString('\n')
		if err != nil {
			stdin.Close()
			_, _ = io.Copy(io.Discard, out)
			if werr := sess.Wait(); werr != nil {
				return "", werr
			}
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	var probe []string
	for {
		line, err := readLine()
		if err != nil {
			return fatalf("ssh %s: %v", *target, err)
		}
		if line == "PVM-PROBED" {
			break
		}
		probe = append(probe, line)
	}
	t, err := parseDeployProbe(strings.Join(probe, "\n"))
	if err != nil {
		return stop("%v", err)
	}
	if *userMode && t.root {
		return stop("--user-mode needs a regular ssh user, not root")
	}
	// Without lingering, systemd stops the user service when this ssh
	// session ends, before the exit is added, and the invite is wasted.
	if *userMode && !t.linger {
		return stop("--user-mode: lingering is off for %s, so its services stop when this ssh session ends; ask an administrator of the VPS to run `sudo loginctl enable-linger %s` and rerun, or deploy as root or a sudo user without --user-mode; nothing was changed", t.user, t.user)
	}
	useSudo := !*userMode && !t.root
	if useSudo && t.sudo == "none" {
		return stop("%s is not root and the VPS has no sudo: connect as root, or use --user-mode", t.user)
	}
	if useSudo && t.sudo == "pass" && !isTTY(os.Stdin) {
		return stop("sudo on %s needs %s's password and this is not a terminal: rerun in a terminal to type it, allow the commands without a password, or use --user-mode", *target, t.user)
	}

	upload := ""
	if t.packaged {
		if *binary != "" || (*tag != "" && *tag != t.installed) {
			return stop("the VPS has pvm-exit %s from the .deb package; upgrade it there (apt install ./pvm-exit_<version>_%s.deb) and rerun, or deploy without --binary/--tag to use it as is", t.installed, t.arch)
		}
		fmt.Printf("pvm-exit %s is installed from the package\n", t.installed)
	} else if *binary != "" {
		if err := checkLinuxBinary(*binary, t.arch); err != nil {
			return stop("--binary: %v", err)
		}
		upload = *binary
	} else if want := releaseWanted(*tag); want == "" {
		return stop("this pvm (%s) is not a release, so there is no matching pvm-exit to download: pass --tag vX.Y.Z or --binary <linux pvm-exit>", buildinfo.Version)
	} else if t.installed != want {
		tmp, err := os.MkdirTemp("", "pvm-exit-deploy-*")
		if err != nil {
			return stop("%v", err)
		}
		defer os.RemoveAll(tmp)
		if upload, err = fetchExitRelease(want, t.arch, tmp); err != nil {
			return stop("%v", err)
		}
	} else {
		fmt.Printf("pvm-exit %s is already installed\n", t.installed)
	}
	var bin []byte
	if upload != "" {
		if bin, err = os.ReadFile(upload); err != nil {
			return stop("%v", err)
		}
	}

	fmt.Fprintln(stdin, "go")
	switch {
	case !useSudo:
		fmt.Fprintln(stdin, "plain")
	case t.sudo == "pass":
		fmt.Fprintln(stdin, "sudo-pw")
		for try := 1; ; try++ {
			fmt.Fprintf(os.Stderr, "[sudo] password for %s on %s: ", t.user, sshHost(*target))
			pw, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return stop("read password: %v", err)
			}
			_, werr := stdin.Write(append(pw, '\n'))
			clear(pw)
			if werr != nil {
				return stop("ssh: %v", werr)
			}
			reply, err := readLine()
			if err != nil {
				return fatalf("ssh %s: %v", *target, err)
			}
			if reply == "PVM-SUDO-OK" {
				break
			}
			if reply == "PVM-SUDO-TTY" {
				_ = sess.Wait()
				return fatalf("sudo on %s requires a terminal (requiretty), which this deploy cannot give it; nothing was changed. Allow the commands without a password (NOPASSWD), drop requiretty for %s, or use --user-mode", *target, t.user)
			}
			if try == 3 {
				stdin.Close()
				_, _ = io.Copy(io.Discard, out)
				_ = sess.Wait()
				return fatalf("sudo on %s: 3 incorrect password attempts; nothing was changed", *target)
			}
			fmt.Fprintln(os.Stderr, "Sorry, try again.")
		}
	default:
		fmt.Fprintln(stdin, "sudo")
	}

	script := deployScript(upload != "", t.path, useSudo, *userMode, *migrate, host, clientName)
	filter := &inviteFilter{out: os.Stdout}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(filter, out)
		close(copied)
	}()
	fmt.Fprintln(stdin, base64.StdEncoding.EncodeToString([]byte(script)))
	if bin != nil {
		fmt.Printf("uploading pvm-exit (%s)...\n", t.arch)
		fmt.Fprintln(stdin, "bin")
		_, _ = stdin.Write(bin)
	} else {
		fmt.Fprintln(stdin, "none")
	}
	stdin.Close()
	<-copied
	werr := sess.Wait()
	filter.Close()
	if werr != nil {
		return fatalf("setting up pvm-exit on %s failed: %v", *target, werr)
	}
	if filter.invite == "" {
		return fatalf("the VPS returned no invite; inspect it with `ssh %s sudo pvm-exit info`", *target)
	}

	add := []string{name, "--invite", filter.invite, "--transport", *transport}
	if *tz != "" {
		add = append(add, "--tz", *tz)
	}
	if *locale != "" {
		add = append(add, "--locale", *locale)
	}
	return exitAdd(add)
}

func sshHost(target string) string {
	h := target
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return strings.Trim(h, "[]")
}

func defaultClientName() string {
	h, err := os.Hostname()
	if err != nil {
		return "pvm-host"
	}
	h = regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(h, "-")
	h = strings.TrimLeft(h, "._-")
	if len(h) > 64 {
		h = h[:64]
	}
	if h == "" {
		return "pvm-host"
	}
	return h
}

func parseDeployProbe(out string) (deployTarget, error) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")), "\n")
	if len(lines) < 4 {
		return deployTarget{}, errors.New("unexpected probe output from the VPS")
	}
	if lines[0] != "Linux" {
		return deployTarget{}, fmt.Errorf("the VPS runs %s; pvm-exit needs Linux", lines[0])
	}
	var t deployTarget
	switch lines[1] {
	case "x86_64", "amd64":
		t.arch = "amd64"
	case "aarch64", "arm64":
		t.arch = "arm64"
	default:
		return deployTarget{}, fmt.Errorf("unsupported VPS architecture %s", lines[1])
	}
	t.root = lines[2] == "0"
	t.user = lines[3]
	for _, l := range lines[4:] {
		if v, ok := strings.CutPrefix(l, "pvm-exit="); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				t.installed = f[0]
			}
			if len(f) > 1 {
				t.path = f[1]
			}
			t.packaged = t.path == "/usr/bin/pvm-exit"
		} else if v, ok := strings.CutPrefix(l, "sudo="); ok {
			t.sudo = v
		} else if v, ok := strings.CutPrefix(l, "linger="); ok {
			t.linger = v == "yes"
		}
	}
	if t.sudo == "" {
		return deployTarget{}, errors.New("unexpected probe output from the VPS")
	}
	return t, nil
}

func checkLinuxBinary(path, arch string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s is not a linux executable: %w", path, err)
	}
	defer f.Close()
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[arch]
	if f.Machine != want {
		return fmt.Errorf("%s is built for %s, the VPS is %s", path, f.Machine, arch)
	}
	return nil
}

// fetchExitRelease downloads pvm-exit for arch and checks its provenance
// and checksum here, so the VPS needs no GitHub CLI.
func fetchExitRelease(tag, arch, dir string) (string, error) {
	if !releaseTagRE.MatchString(tag) {
		return "", fmt.Errorf("invalid release tag %q", tag)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 30 * time.Second}}
	asset := "pvm-exit_linux_" + arch + ".tar.gz"
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", upgradeRepo, tag)
	arc, sums := filepath.Join(dir, asset), filepath.Join(dir, "SHA256SUMS")
	fmt.Printf("downloading %s (%s)...\n", asset, tag)
	if err := downloadFile(client, base+asset, arc); err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	if err := downloadFile(client, base+"SHA256SUMS", sums); err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	for _, p := range []string{sums, arc} {
		if err := verifyReleaseAttestation(p, tag); err != nil {
			return "", fmt.Errorf("release provenance: %w", err)
		}
	}
	if err := verifyArchiveSum(arc, sums, asset); err != nil {
		return "", fmt.Errorf("checksum: %w", err)
	}
	bin := filepath.Join(dir, "pvm-exit")
	if err := untarSingle(arc, "pvm-exit", bin); err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}
	return bin, checkLinuxBinary(bin, arch)
}

// deployScript is sourced by deployBootstrap, which defines S (run as
// root) and puts an uploaded binary at "$d/pvm-exit". It installs that
// binary or uses the one at installed, initializes the service unless it
// is already set up, and prints the invite behind inviteMarker so the
// caller can keep it off the terminal.
func deployScript(uploaded bool, installed string, useSudo, userMode, migrate bool, host, client string) string {
	var b bytes.Buffer
	b.WriteString("set -e\n")
	s := ""
	if useSudo {
		s = "S "
	}
	dest := "/usr/local/bin/pvm-exit"
	mode := ""
	if userMode {
		dest = `"$HOME/.local/bin/pvm-exit"`
		mode = " --user"
	}
	if uploaded {
		if userMode {
			b.WriteString(`mkdir -p "$HOME/.local/bin"` + "\n")
		}
		fmt.Fprintf(&b, "%sinstall -m 0755 \"$d/pvm-exit\" %s\n", s, dest)
		fmt.Fprintf(&b, "echo \"installed $(%s version)\"\n", dest)
	} else {
		dest = shellQuoteArg(installed)
		if installed == "/usr/bin/pvm-exit" {
			mode = " --packaged"
		}
	}
	initArgs := "init" + mode
	if migrate {
		initArgs += " --migrate"
	}
	// An existing setup keeps its config. init without flags rewrites the
	// unit and (re)starts the service: after an upload, so it runs the new
	// binary, and when the service is not running.
	rerun := s + dest + " init" + mode
	running := `echo "pvm-exit is already set up"`
	if uploaded {
		running = rerun
	}
	fmt.Fprintf(&b, "st=$(%s%s info --json 2>/dev/null) || st=\n", s, dest)
	fmt.Fprintf(&b, "case \"$st\" in\n'') %s%s %s --public-endpoint %s ;;\n*'\"service\":\"active\"'*) %s ;;\n*) %s ;;\nesac\n",
		s, dest, initArgs, shellQuoteArg(host), running, rerun)
	fmt.Fprintf(&b, "inv=$(%s%s invite %s --raw --host %s)\n", s, dest, shellQuoteArg(client), shellQuoteArg(host))
	fmt.Fprintf(&b, "echo \"%s$inv\"\n", inviteMarker)
	return b.String()
}

func shellQuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// inviteFilter passes the remote output through as it arrives, indented,
// and keeps the inviteMarker line for itself. It works on bytes, not lines,
// so a partial line shows as soon as it arrives.
type inviteFilter struct {
	out     io.Writer
	state   int // 0 line start, 1 matching the marker, 2 passing a line, 3 capturing the invite
	pending []byte
	capture []byte
	invite  string
}

func (f *inviteFilter) Write(p []byte) (int, error) {
	var buf []byte
	for _, b := range p {
		switch f.state {
		case 0, 1:
			f.state = 1
			f.pending = append(f.pending, b)
			if !strings.HasPrefix(inviteMarker, string(f.pending)) {
				buf = append(append(buf, "  "...), f.pending...)
				f.pending, f.state = f.pending[:0], 2
				if b == '\n' {
					f.state = 0
				}
			} else if len(f.pending) == len(inviteMarker) {
				f.pending, f.state = f.pending[:0], 3
			}
		case 2:
			buf = append(buf, b)
			if b == '\n' {
				f.state = 0
			}
		case 3:
			if b == '\n' {
				f.invite = strings.TrimRight(string(f.capture), "\r")
				f.capture, f.state = f.capture[:0], 0
			} else {
				f.capture = append(f.capture, b)
			}
		}
	}
	if len(buf) > 0 {
		if _, err := f.out.Write(buf); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close flushes a partial line that looked like the start of the marker.
func (f *inviteFilter) Close() {
	if f.state == 1 && len(f.pending) > 0 {
		_, _ = f.out.Write(append([]byte("  "), f.pending...))
	}
	if f.state == 3 && f.invite == "" {
		f.invite = strings.TrimRight(string(f.capture), "\r")
	}
}
