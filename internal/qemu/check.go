package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/native"
)

// MinVersion is the oldest QEMU pvm runs on this host. darwin and windows
// need 11.1 (HVF live restore was fixed in 11.1.0, and pvm's WHPX arguments
// were validated there); Linux accepts the QEMU of current LTS releases.
func MinVersion() [3]int {
	if runtime.GOOS == "linux" {
		return [3]int{8, 2, 0}
	}
	return [3]int{11, 1, 0}
}

var versionRE = regexp.MustCompile(`QEMU emulator version ([0-9]+)\.([0-9]+)\.([0-9]+)`)

// parseVersion reads "QEMU emulator version X.Y.Z" from --version output.
func parseVersion(out string) ([3]int, bool) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionString(v [3]int) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	fsutil.ConfigureHiddenCmd(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s %s timed out", filepath.Base(name), strings.Join(args, " "))
	}
	return string(out), err
}

// CheckError says why a build failed its checks, with the next step.
type CheckError struct {
	Build  string // what was checked, e.g. "QEMU from Homebrew (/opt/homebrew/bin)"
	Reason string
	Next   string
}

func (e *CheckError) Error() string {
	msg := fmt.Sprintf("%s failed its check: %s", e.Build, e.Reason)
	if e.Next != "" {
		msg += "; " + e.Next
	}
	return msg
}

// Check runs every check a build must pass before pvm uses it: the
// minimum version for this host, the help probes for what pvm's argv
// needs, the firmware, macOS's hypervisor entitlement, and a start of
// pvm's own argv. No build is exempt because of where it is installed.
func Check(b *Build) error {
	what := b.Label()
	if b.Dir != "" {
		what = fmt.Sprintf("%s (%s)", b.ID, b.Dir)
	} else {
		what = fmt.Sprintf("%s (%s)", ChannelLabel(b.Channel), filepath.Dir(b.System))
	}
	fail := func(reason, next string) error { return &CheckError{Build: what, Reason: reason, Next: next} }
	next := "use pvm qemu install"
	if b.Source == SourceProject {
		next = fmt.Sprintf("reinstall it: pvm qemu rm %s -y && pvm qemu install %s --use", b.ID, b.ID)
	}

	out, err := run(10*time.Second, b.System, "--version")
	v, ok := parseVersion(out)
	if err != nil || !ok {
		return fail(fmt.Sprintf("%s --version did not run (%s)", filepath.Base(b.System), firstLine(strings.TrimSpace(out+" "+errString(err)))), next)
	}
	if min := MinVersion(); versionLess(v, min) {
		return fail(fmt.Sprintf("QEMU %s is older than %s, the oldest pvm supports on %s", versionString(v), versionString(min), runtime.GOOS), next)
	}
	b.Version = versionString(v)
	b.VersionLine = firstLine(strings.TrimSpace(out))
	if out, err := run(10*time.Second, b.Img, "--version"); err != nil || !strings.Contains(out, "qemu-img version") {
		return fail("qemu-img --version did not run", next)
	}
	caps, err := Probe(b.System)
	if err != nil {
		return fail(err.Error(), next)
	}
	b.Caps = caps
	fw := firmwareFile()
	if fi, err := os.Stat(filepath.Join(b.FirmwareDir, fw)); err != nil || fi.Size() == 0 {
		return fail(fmt.Sprintf("the firmware %s is not in %s", fw, b.FirmwareDir), next)
	}
	if err := checkEntitlement(b.System); err != nil {
		return fail(err.Error(), "use brew install qemu or pvm qemu install")
	}
	if err := Smoke(b); err != nil {
		return fail("pvm's start test: "+err.Error(), next)
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firmwareFile() string {
	if native.Arch == "arm64" {
		return "edk2-aarch64-code.fd"
	}
	return "bios-256k.bin"
}

// helpWords lists the names in one `-<opt> help` listing.
func helpWords(system, opt string) (map[string]bool, string, error) {
	args := []string{"-machine", "none", opt, "help"}
	if opt == "-machine" {
		args = []string{opt, "help"}
	}
	out, err := run(10*time.Second, system, args...)
	if err != nil {
		return nil, out, fmt.Errorf("%s help failed: %s", opt, firstLine(strings.TrimSpace(out)))
	}
	words := map[string]bool{}
	if opt == "-device" {
		for _, m := range regexp.MustCompile(`(?m)^name "([^"]+)"`).FindAllStringSubmatch(out, -1) {
			words[m[1]] = true
		}
		return words, out, nil
	}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue // the header
		}
		if f := strings.Fields(line); len(f) > 0 {
			words[f[0]] = true
		}
	}
	return words, out, nil
}

// Probe asks a QEMU what it supports and requires everything pvm's argv
// uses. virtio-9p is required on macOS and Linux and recorded on Windows,
// where no QEMU has it.
func Probe(system string) (Caps, error) {
	var caps Caps
	accels, _, err := helpWords(system, "-accel")
	if err != nil {
		return caps, err
	}
	nativeAccel := map[string]string{"darwin": "hvf", "linux": "kvm", "windows": "whpx"}[runtime.GOOS]
	for _, a := range []string{nativeAccel, "tcg"} {
		if !accels[a] {
			return caps, fmt.Errorf("it has no %s accelerator", a)
		}
	}
	for a := range accels {
		caps.Accels = append(caps.Accels, a)
	}
	machines, _, err := helpWords(system, "-machine")
	if err != nil {
		return caps, err
	}
	machine := "q35"
	if native.Arch == "arm64" {
		machine = "virt"
	}
	if !machines[machine] {
		return caps, fmt.Errorf("it has no %s machine", machine)
	}
	netdevs, _, err := helpWords(system, "-netdev")
	if err != nil {
		return caps, err
	}
	for _, n := range []string{"user", "stream"} {
		if !netdevs[n] {
			return caps, fmt.Errorf("it has no %s netdev (built without libslirp?)", n)
		}
	}
	devices, _, err := helpWords(system, "-device")
	if err != nil {
		return caps, err
	}
	for _, d := range []string{"virtio-net-pci", "virtio-rng-pci", "virtio-serial-pci", "virtserialport", "virtio-balloon-pci", "virtio-gpu-pci", "pcie-root-port"} {
		if !devices[d] {
			return caps, fmt.Errorf("it has no %s device", d)
		}
	}
	caps.Virtio9p = devices["virtio-9p-pci-non-transitional"]
	if !caps.Virtio9p && runtime.GOOS != "windows" {
		return caps, fmt.Errorf("it has no virtio-9p (built without VirtFS)")
	}
	objects, _, err := helpWords(system, "-object")
	if err != nil {
		return caps, err
	}
	rng := "rng-random"
	if runtime.GOOS == "windows" {
		rng = "rng-builtin"
	}
	if !objects[rng] {
		return caps, fmt.Errorf("it has no %s object", rng)
	}
	return caps, nil
}
