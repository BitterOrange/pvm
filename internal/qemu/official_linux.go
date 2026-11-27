//go:build linux

package qemu

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// DetectOfficial finds the distribution's QEMU in /usr/bin and requires a
// package to own it: a binary installed by hand (make install, a copy) is
// not an official build.
func DetectOfficial() (*Official, error) {
	system, img := "/usr/bin/qemu-system-x86_64", "/usr/bin/qemu-img"
	if _, err := os.Stat(system); err != nil {
		if _, err := os.Stat("/usr/libexec/qemu-kvm"); err == nil {
			return nil, officialMissing("this distribution ships QEMU as /usr/libexec/qemu-kvm, which is not a supported official channel; use pvm qemu install")
		}
		return nil, officialMissing("your distribution's QEMU is not installed; run sudo apt install qemu-system-x86 qemu-utils (Debian, Ubuntu), sudo dnf install qemu-system-x86-core qemu-img (Fedora) or sudo pacman -S qemu-base (Arch), then pvm qemu use official")
	}
	if _, err := os.Stat(img); err != nil {
		return nil, officialMissing("/usr/bin/qemu-img is missing; install your distribution's qemu-img (qemu-utils on Debian and Ubuntu)")
	}
	real := resolveLink(system)
	owner, version := ownerOf(real)
	if owner == "" {
		owner, version = ownerOf(system)
	}
	if owner == "" {
		return nil, officialMissing("%s belongs to no Debian, Fedora or Arch package, so it is not an official build; use pvm qemu install", system)
	}
	return &Official{
		Channel: "distro", System: system, SystemExec: real, Img: img,
		FirmwareDir: "/usr/share/qemu", PackageVersion: owner + " " + version,
	}, nil
}

// ownerOf asks dpkg, rpm or pacman which package owns path.
func ownerOf(path string) (pkg, version string) {
	try := func(name string, args ...string) string {
		if _, err := exec.LookPath(name); err != nil {
			return ""
		}
		out, err := run(10*time.Second, name, args...)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}
	if out := try("dpkg-query", "-S", path); out != "" && !strings.HasPrefix(out, "diversion") {
		pkg = strings.SplitN(strings.SplitN(out, ":", 2)[0], ",", 2)[0]
		return pkg, try("dpkg-query", "-W", "-f", "${Version}", pkg)
	}
	if out := try("rpm", "-qf", "--qf", "%{NAME} %{VERSION}-%{RELEASE}", path); out != "" {
		f := strings.Fields(out)
		if len(f) == 2 {
			return f[0], f[1]
		}
	}
	if out := try("pacman", "-Qo", path); out != "" {
		f := strings.Fields(out)
		if len(f) >= 2 {
			return f[len(f)-2], f[len(f)-1]
		}
	}
	return "", ""
}
