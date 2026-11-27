package qemu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Build sources.
const (
	SourceProject  = "project"  // a pvm-qemu release build pinned by this pvm
	SourceOfficial = "official" // the platform channel's own QEMU
)

// OfficialID is the build id of the registered official QEMU.
const OfficialID = "official"

// BuildIdentity names one QEMU binary exactly. A version string cannot tell
// a stock 11.1.3 from a patched one, and a package manager can replace a
// binary under the same path, so the identity carries the hash of the
// executable that actually runs.
type BuildIdentity struct {
	ID           string `json:"id"`                // pvm-qemu tag, or "official"
	Source       string `json:"source"`            // SourceProject or SourceOfficial
	Channel      string `json:"channel,omitempty"` // official only: homebrew, distro or msys2
	Version      string `json:"version"`           // QEMU version, e.g. 11.1.3
	SystemSHA256 string `json:"system_sha256"`
}

func (b BuildIdentity) String() string {
	sha := b.SystemSHA256
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return b.ID + "@" + sha
}

// Same reports whether two identities name the same binary.
func (b *BuildIdentity) Same(o *BuildIdentity) bool {
	return b != nil && o != nil && b.ID == o.ID && b.SystemSHA256 == o.SystemSHA256
}

// Label is the human name of a build: the channel for official builds.
func (b BuildIdentity) Label() string {
	if b.Source == SourceProject {
		return fmt.Sprintf("%s (project build, QEMU %s)", b.ID, b.Version)
	}
	return fmt.Sprintf("%s, QEMU %s", ChannelLabel(b.Channel), b.Version)
}

// ChannelLabel names an official channel the way users know it; none of
// these channels is the QEMU project itself.
func ChannelLabel(channel string) string {
	switch channel {
	case "homebrew":
		return "QEMU from Homebrew"
	case "distro":
		return "your distribution's QEMU"
	case "msys2":
		return "QEMU from MSYS2 UCRT64"
	}
	return "official QEMU"
}

// HostKey names this host the way pvm-qemu names its archives.
func HostKey() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
