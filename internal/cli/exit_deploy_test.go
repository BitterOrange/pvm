package cli

import (
	"encoding/base64"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestParseDeployProbe(t *testing.T) {
	got, err := parseDeployProbe("Linux\r\nx86_64\n1000\nme\npvm-exit=v1.4.1 /usr/local/bin/pvm-exit\nlinger=yes\nsudo=pass\n")
	if err != nil || got != (deployTarget{arch: "amd64", user: "me", sudo: "pass", installed: "v1.4.1", path: "/usr/local/bin/pvm-exit", linger: true}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = parseDeployProbe("Linux\nx86_64\n1000\nme\nlinger=no\nsudo=pass\n")
	if err != nil || got.linger {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = parseDeployProbe("Linux\nx86_64\n0\nroot\npvm-exit=v1.4.1 /usr/bin/pvm-exit\nsudo=root\n")
	if err != nil || got != (deployTarget{arch: "amd64", root: true, user: "root", sudo: "root", installed: "v1.4.1", path: "/usr/bin/pvm-exit", packaged: true}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = parseDeployProbe("Linux\naarch64\n1000\nme\nsudo=none\n")
	if err != nil || got != (deployTarget{arch: "arm64", user: "me", sudo: "none"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, bad := range []string{"", "Darwin\narm64\n501\nme\nsudo=pass\n", "Linux\nmips\n0\nroot\nsudo=root\n", "Linux\nx86_64\n0\nroot\n"} {
		if _, err := parseDeployProbe(bad); err == nil {
			t.Fatalf("%q: want error", bad)
		}
	}
}

func linuxSh(t *testing.T) string {
	sh, err := exec.LookPath("sh")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("runs on the Linux side of a deploy")
	}
	return sh
}

func TestDeployProbeWithoutPvmExit(t *testing.T) {
	cmd := exec.Command(linuxSh(t), "-c", deployProbe)
	cmd.Env = append(cmd.Environ(), "PATH=/nonexistent:/usr/bin:/bin", "HOME="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe failed without pvm-exit: %v %q", err, out)
	}
	if got, err := parseDeployProbe(string(out)); err != nil || got.installed != "" {
		t.Fatalf("got %+v, %v from %q", got, err, out)
	}
}

func TestDeployBootstrap(t *testing.T) {
	sh := linuxSh(t)
	run := func(stdin string) string {
		cmd := exec.Command(sh, "-c", deployBootstrap)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bootstrap: %v\n%s", err, out)
		}
		return string(out)
	}
	if out := run("stop\n"); !strings.HasSuffix(out, "PVM-PROBED\n") {
		t.Fatalf("stop should end right after the probe:\n%s", out)
	}
	script := `echo "got $(wc -c < "$d/pvm-exit")"; S echo ran-as-S; read -r x || echo stdin-closed`
	out := run("go\nplain\n" + base64.StdEncoding.EncodeToString([]byte(script)) + "\nbin\nBINARY\x00DATA")
	for _, want := range []string{"PVM-PROBED\n", "got 11\n", "ran-as-S\n", "stdin-closed\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func TestSSHHost(t *testing.T) {
	for in, want := range map[string]string{
		"root@vps.example.com":  "vps.example.com",
		"vps.example.com":       "vps.example.com",
		"me@203.0.113.7":        "203.0.113.7",
		"me@[2001:db8::1]:2222": "2001:db8::1",
		"a@b@vps.example.com":   "vps.example.com",
		"vps.example.com:22":    "vps.example.com",
	} {
		if got := sshHost(in); got != want {
			t.Errorf("sshHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeployScript(t *testing.T) {
	cases := []struct {
		name                string
		uploaded            bool
		installed           string
		sudo, user, migrate bool
		want, notWant       []string
	}{
		{"root upload", true, "", false, false, false,
			[]string{`install -m 0755 "$d/pvm-exit" /usr/local/bin/pvm-exit`, `*'"service":"active"'*) /usr/local/bin/pvm-exit init ;;`, "init --public-endpoint 'vps.example.com'", "invite 'dev1' --raw --host 'vps.example.com'", inviteMarker},
			[]string{"S ", "sudo", "--user", "--migrate", "--packaged"}},
		{"sudo migrate, installed", false, "/usr/local/bin/pvm-exit", true, false, true,
			[]string{"S '/usr/local/bin/pvm-exit' info --json", "already set up", "*) S '/usr/local/bin/pvm-exit' init ;;", "S '/usr/local/bin/pvm-exit' init --migrate", "S '/usr/local/bin/pvm-exit' invite"},
			[]string{"install -m", "--packaged", "sudo"}},
		{"packaged", false, "/usr/bin/pvm-exit", false, false, false,
			[]string{"'/usr/bin/pvm-exit' init --packaged --public-endpoint", "already set up"},
			[]string{"install -m", "/usr/local/bin"}},
		{"user mode", true, "", false, true, false,
			[]string{`mkdir -p "$HOME/.local/bin"`, `"$HOME/.local/bin/pvm-exit" init --user`, `*'"service":"active"'*) "$HOME/.local/bin/pvm-exit" init --user ;;`},
			[]string{"S ", "sudo", "/usr/local/bin"}},
	}
	for _, c := range cases {
		s := deployScript(c.uploaded, c.installed, c.sudo, c.user, c.migrate, "vps.example.com", "dev1")
		for _, w := range c.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: missing %q in\n%s", c.name, w, s)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(s, w) {
				t.Errorf("%s: unexpected %q in\n%s", c.name, w, s)
			}
		}
		if sh, err := exec.LookPath("sh"); err == nil {
			if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
				t.Errorf("%s: sh -n: %v %s", c.name, err, out)
			}
		}
	}
}

func TestDefaultClientName(t *testing.T) {
	if n := defaultClientName(); n == "" || len(n) > 64 || strings.ContainsAny(n, " /") {
		t.Fatalf("defaultClientName() = %q", n)
	}
}

func TestInviteFilter(t *testing.T) {
	var out strings.Builder
	f := &inviteFilter{out: &out}
	for _, chunk := range []string{"installed v1", ".4.1\n[sudo] pass", "word for u: "} {
		f.Write([]byte(chunk))
	}
	// a partial line shows as soon as it arrives
	if got := out.String(); got != "  installed v1.4.1\n  [sudo] password for u: " {
		t.Fatalf("before the newline: %q", got)
	}
	for _, chunk := range []string{"\r\nready\r\nPVM-INV", "ITE pvmx1:abc\r\n", "PVM-other\n"} {
		f.Write([]byte(chunk))
	}
	f.Close()
	if f.invite != "pvmx1:abc" {
		t.Fatalf("invite %q", f.invite)
	}
	if got := out.String(); strings.Contains(got, "pvmx1") || !strings.HasSuffix(got, "\r\n  ready\r\n  PVM-other\n") {
		t.Fatalf("output %q", got)
	}
}
