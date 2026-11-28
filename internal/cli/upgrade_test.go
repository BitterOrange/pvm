package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeRestoresAllVMsAfterFailure(t *testing.T) {
	var got []string
	err := restoreUpgradeVMs([]string{"dev1", "dev2", "dev3"}, func() int { return 0 }, func(name string) int {
		got = append(got, name)
		if name == "dev1" {
			return 1
		}
		return 0
	})
	if err == nil || !strings.Contains(err.Error(), "dev1") || !reflect.DeepEqual(got, []string{"dev1", "dev2", "dev3"}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestUpgradeRecoveryPreservesOriginal(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "pvm")
	if err := os.WriteFile(exe, []byte("old-binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := saveUpgradeRecovery(exe, upgradeRecovery{Version: "v1.0.0", Running: []string{"dev1"}, DaemonRunning: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(exe + ".upgrade.json")
	if err != nil {
		t.Fatal(err)
	}
	var state upgradeRecovery
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(exe + ".previous-" + state.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(old)
	if string(old) != "old-binary" || state.SHA256 != hex.EncodeToString(sum[:]) || !reflect.DeepEqual(state.Running, []string{"dev1"}) {
		t.Fatal("incomplete recovery point")
	}
	if err := os.WriteFile(exe, []byte("new-binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := saveUpgradeRecovery(exe, upgradeRecovery{Version: "v2.0.0"}); err != nil {
		t.Fatal(err)
	}
	preserved, err := os.ReadFile(exe + ".previous-" + state.SHA256)
	if err != nil || string(preserved) != "old-binary" {
		t.Fatal("lost earlier rollback binary", err)
	}
}

func TestUpgradeRejectsNonExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pvm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho v1.0.0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeBinary(path, "v1.0.0"); err == nil {
		t.Fatal("accepted shell in place of Go binary")
	}
}

func writeUpgradeArchive(t *testing.T, path string, files map[string]string) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{tw.Close(), gz.Close(), f.Close()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// install.sh replaces the binary only after every check passes, then sets
// QEMU up with the installed pvm; a failed QEMU setup keeps the new binary.
func TestInstallerChecksBeforeReplacingAndSetsUpQEMU(t *testing.T) {
	for _, scenario := range []string{"attestation-fails", "binary-version-fails", "binary-platform-fails", "unsafe-archive", "binary-rename-fails", "project", "official", "none", "qemu-setup-fails", "bad-pvm-qemu", "old-tag"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			install := filepath.Join(dir, "install")
			fixtures := filepath.Join(dir, "fixtures")
			for _, p := range []string{bin, install, fixtures} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(install, "pvm"), "old-binary")
			qemuLog := filepath.Join(dir, "qemu.log")
			version := "v1.4.1"
			if scenario == "binary-version-fails" {
				version = "v9.9.9"
			}
			binaryFiles := map[string]string{"pvm": "#!/bin/sh\nif [ \"$1\" = version ]; then echo " + version + "; exit 0; fi\n" +
				"echo \"$PVM_BACKEND|$*\" >> \"$QEMU_LOG\"\n[ \"$SCENARIO\" != qemu-setup-fails ]\n"}
			if scenario == "unsafe-archive" {
				binaryFiles["../escaped"] = "must not extract"
			}
			sum := writeUpgradeArchive(t, filepath.Join(fixtures, "pvm_darwin_arm64.tar.gz"), binaryFiles)
			write(filepath.Join(fixtures, "SHA256SUMS"), sum+"  pvm_darwin_arm64.tar.gz\n")
			write(filepath.Join(bin, "file"), "#!/bin/sh\nif [ \"$SCENARIO\" = binary-platform-fails ]; then echo 'ELF 64-bit executable aarch64'; else echo 'Mach-O 64-bit executable arm64'; fi\n")
			write(filepath.Join(bin, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo Darwin;; -m) echo arm64;; esac\n")
			write(filepath.Join(bin, "curl"), `#!/bin/sh
out= hdr= url=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift ;;
    -D) hdr=$2; shift ;;
    -w|-H) shift ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
case "$url" in
  */attestations/sha256:*)
    printf 'HTTP/2 200\r\n\r\n' > "$hdr"
    printf '{\n  "attestations": [\n    {\n      "bundle": {"mediaType":"fake"}\n    }\n  ]\n}\n' > "$out"
    printf 200
    exit 0 ;;
esac
cp "$FIXTURES/${url##*/}" "$out"
`)
			write(filepath.Join(bin, "gh"), `#!/bin/sh
[ "$SCENARIO" != attestation-fails ] || exit 1
while [ $# -gt 0 ]; do
  if [ "$1" = --bundle ]; then grep -q '"mediaType":"fake"' "$2"; exit; fi
  shift
done
exit 1
`)
			mv, err := exec.LookPath("mv")
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(bin, "mv"), fmt.Sprintf(`#!/bin/sh
if [ "$SCENARIO" = binary-rename-fails ] && [ "$1" = -f ] && [ "$3" = "$INSTALL_DIR/pvm" ]; then exit 1; fi
exec %q "$@"
`, mv))
			script, err := filepath.Abs("../../scripts/install.sh")
			if err != nil {
				t.Fatal(err)
			}
			tag, mode := "v1.4.1", ""
			switch scenario {
			case "official", "none":
				mode = scenario
			case "bad-pvm-qemu":
				mode = "bundled"
			case "old-tag":
				tag = "v1.3.6"
			}
			cmd := exec.Command("sh", script)
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "PVM_QEMU=") && !strings.HasPrefix(e, "PVM_BACKEND=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, "PATH="+bin+":/usr/bin:/bin", "INSTALL_DIR="+install, "TAG="+tag,
				"FIXTURES="+fixtures, "SCENARIO="+scenario, "QEMU_LOG="+qemuLog)
			if mode != "" {
				cmd.Env = append(cmd.Env, "PVM_QEMU="+mode)
			}
			out, err := cmd.CombinedOutput()
			calls, _ := os.ReadFile(qemuLog)
			installed, _ := os.ReadFile(filepath.Join(install, "pvm"))
			switch scenario {
			case "bad-pvm-qemu", "old-tag":
				if err == nil || strings.Contains(string(out), "downloading") {
					t.Fatalf("must refuse before any download: %v: %s", err, out)
				}
				want := map[string]string{"bad-pvm-qemu": "invalid PVM_QEMU=bundled", "old-tag": "raw.githubusercontent.com/BitterOrange/pvm/v1.3.6/scripts/install.sh"}[scenario]
				if !strings.Contains(string(out), want) {
					t.Fatalf("output lacks %q: %s", want, out)
				}
				return
			case "project", "official", "none":
				if err != nil {
					t.Fatalf("install: %v: %s", err, out)
				}
				if !strings.Contains(string(installed), "v1.4.1") {
					t.Fatal("new binary not installed")
				}
				want := map[string]string{"project": "|qemu install -y\n", "official": "|qemu use official -y\n", "none": ""}[scenario]
				if string(calls) != want {
					t.Fatalf("qemu calls %q, want %q", calls, want)
				}
				if scenario == "none" && !strings.Contains(string(out), "pvm qemu install") {
					t.Fatalf("none must print the setup commands: %s", out)
				}
				if matches, _ := filepath.Glob(filepath.Join(install, "pvm.previous.*")); len(matches) != 1 {
					t.Fatal("missing old binary")
				}
			case "qemu-setup-fails":
				if err == nil || !strings.Contains(string(out), "pvm is installed; QEMU setup failed: retry pvm qemu install") {
					t.Fatalf("a failed QEMU setup must fail visibly: %v: %s", err, out)
				}
				if !strings.Contains(string(installed), "v1.4.1") {
					t.Fatal("a failed QEMU setup must keep the new binary")
				}
			default:
				if !strings.Contains(string(out), "downloading pvm_darwin_arm64.tar.gz") {
					t.Fatalf("installer did not reach download: %v: %s", err, out)
				}
				if err == nil {
					t.Fatal("expected failure")
				}
				if string(installed) != "old-binary" {
					t.Fatal("failure changed installed binary")
				}
				if len(calls) != 0 {
					t.Fatalf("QEMU set up after a failed install: %q", calls)
				}
			}
			if _, err := os.Stat(filepath.Join(install, "escaped")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("archive escaped staging directory", err)
			}
			if _, err := os.Stat(filepath.Join(install, ".pvm-install.lock")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("stale installer lock", err)
			}
		})
	}
}

// The attestation comes from the API without a login and goes to gh as a
// bundle; every API failure stops the check with its own reason.
func TestReleaseAttestationPolicy(t *testing.T) {
	dir := t.TempDir()
	log, got := filepath.Join(dir, "args"), filepath.Join(dir, "bundle")
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ATTEST_ARGS\"\n" +
		"while [ $# -gt 0 ]; do [ \"$1\" = --bundle ] && cat \"$2\" > \"$ATTEST_BUNDLE\"; shift; done\n" +
		"echo 'Error: verifying with issuer \"sigstore.dev\"' >&2\nexit \"$ATTEST_EXIT\"\n"
	if err := os.WriteFile(gh, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ATTEST_ARGS", log)
	t.Setenv("ATTEST_BUNDLE", got)
	t.Setenv("ATTEST_EXIT", "0")
	file := filepath.Join(dir, "release.tar.gz")
	if err := os.WriteFile(file, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("release"))

	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sha256:"+hex.EncodeToString(sum[:]) || r.Header.Get("Authorization") != "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		switch mode {
		case "ok":
			fmt.Fprint(w, "{\n  \"attestations\": [\n    {\"bundle_url\": \"x\", \"bundle\": {\"mediaType\": \"a\",\n \"n\": 1}},\n    {\"bundle\": {\"mediaType\": \"b\"}}\n  ]\n}\n")
		case "notfound":
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case "ratelimit":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1795900000")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
		case "forbidden":
			w.Header().Set("X-RateLimit-Remaining", "42")
			http.Error(w, `{"message":"Forbidden"}`, http.StatusForbidden)
		case "server":
			http.Error(w, "oops", http.StatusInternalServerError)
		case "nobundle":
			fmt.Fprint(w, `{"attestations":[]}`)
		case "garbage":
			fmt.Fprint(w, `<html>`)
		}
	}))
	defer srv.Close()
	old := attestationAPI
	t.Cleanup(func() { attestationAPI = old })
	attestationAPI = srv.URL

	if err := verifyReleaseAttestation(file, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--bundle\n", "--repo\n" + upgradeRepo, "--signer-workflow\n" + upgradeRepo + "/.github/workflows/build.yml", "--source-ref\nrefs/tags/v1.2.3", "--deny-self-hosted-runners"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing policy %q", want)
		}
	}
	if b, _ := os.ReadFile(got); string(b) != "{\"mediaType\":\"a\",\"n\":1}\n{\"mediaType\":\"b\"}\n" {
		t.Fatalf("bundle file = %q, want one bundle per line", b)
	}
	t.Setenv("ATTEST_EXIT", "1")
	if err := verifyReleaseAttestation(file, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "sigstore.dev") {
		t.Fatalf("gh failure must stop the check with its reason: %v", err)
	}
	t.Setenv("ATTEST_EXIT", "0")

	for m, want := range map[string]string{
		"notfound":  "has no attestation from " + upgradeRepo,
		"ratelimit": "60 requests an hour without a login",
		"forbidden": "403 Forbidden",
		"server":    "500 Internal Server Error",
		"nobundle":  "holds no bundle",
		"garbage":   "read the attestation",
	} {
		mode = m
		if err := verifyReleaseAttestation(file, "v1.2.3"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", m, err, want)
		}
	}
	srv.Close()
	if err := verifyReleaseAttestation(file, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "cannot reach api.github.com") {
		t.Errorf("unreachable API: err = %v", err)
	}
}

func TestUpgradeBinaryVersionValidation(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "pvm")
	if err := os.WriteFile(source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"v1.2.3\")}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", binary, source)
	cmd.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v: %s", err, out)
	}
	if err := validateUpgradeBinary(binary, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgradeBinary(binary, "v9.9.9"); err == nil {
		t.Fatal("accepted incorrect release version")
	}
	original, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveUpgradeRecovery(binary, upgradeRecovery{Version: "v1.2.3", Running: []string{"dev1"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("broken-new-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if code := rollbackUpgrade(binary, true, true); code != 0 {
		t.Fatalf("rollback failed: %d", code)
	}
	restored, err := os.ReadFile(binary)
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatal("rollback did not restore original executable", err)
	}

}
