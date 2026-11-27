//go:build darwin

package qemu

import (
	"os"
	"path/filepath"
	"strings"
)

// officialFirmwareDirs: this channel keeps all firmware in FirmwareDir.
var officialFirmwareDirs []string

// DetectOfficial finds QEMU from Homebrew. The stable bin path is
// registered, so a brew upgrade is noticed as a changed binary.
func DetectOfficial() (*Official, error) {
	system := "/opt/homebrew/bin/qemu-system-aarch64"
	img := "/opt/homebrew/bin/qemu-img"
	if _, err := os.Stat(system); err != nil {
		return nil, officialMissing("QEMU from Homebrew is not installed; run brew install qemu, then pvm qemu use official")
	}
	real := resolveLink(system)
	rest, ok := strings.CutPrefix(real, "/opt/homebrew/Cellar/qemu/")
	if !ok {
		return nil, officialMissing("%s is not Homebrew's qemu (it resolves to %s); use pvm qemu install", system, real)
	}
	if _, err := os.Stat(img); err != nil {
		return nil, officialMissing("Homebrew's qemu-img is missing; run brew reinstall qemu")
	}
	return &Official{
		Channel: "homebrew", System: system, SystemExec: real, Img: img,
		FirmwareDir:    "/opt/homebrew/share/qemu",
		PackageVersion: strings.SplitN(rest, string(filepath.Separator), 2)[0],
	}, nil
}
