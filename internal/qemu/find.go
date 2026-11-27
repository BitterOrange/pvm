// Package qemu runs pvm's VMs on a QEMU build registered in qemu.json:
// either a pvm-qemu project build or the platform's official QEMU. It never
// looks for QEMU on PATH.
package qemu

import (
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/native"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// Toolchain is a resolved, checked build: what Instance and the disk
// helpers run.
type Toolchain struct {
	System      string // qemu-system-<arch>
	Img         string // qemu-img
	FirmwareDir string // dir containing the firmware
	Version     [3]int
	Caps        Caps
	Build       *BuildIdentity
}

// SupportsVirtio9p reports whether the build has virtio-9p. A toolchain
// without a registered build (tests, fakes) is taken to have it except on
// Windows, where no QEMU does.
func (t *Toolchain) SupportsVirtio9p() bool {
	if t == nil || t.System == "" {
		return true
	}
	if t.Build == nil {
		return runtime.GOOS != "windows"
	}
	return t.Caps.Virtio9p
}

func QemuArch() string { return native.QemuArch() }

// UnusableError means no usable QEMU is selected. The daemon keeps running
// and reports clusters it cannot recover for this reason as blocked; every
// message names the next command.
type UnusableError struct {
	Msg string
}

func (e *UnusableError) Error() string { return e.Msg }

// IsUnusable reports whether err is about the selected QEMU.
func IsUnusable(err error) bool {
	var u *UnusableError
	return errors.As(err, &u)
}

var (
	// ErrNotSetUp: no build is selected yet.
	ErrNotSetUp = &UnusableError{Msg: notSetUpMessage()}
)

func notSetUpMessage() string {
	var second string
	switch runtime.GOOS {
	case "darwin":
		second = "QEMU from Homebrew; brew install qemu first"
	case "linux":
		second = "your distribution's QEMU, 8.2 or newer; sudo apt install qemu-system-x86 qemu-utils first"
	case "windows":
		second = "QEMU from MSYS2 UCRT64, which https://www.qemu.org/download/#windows links to; run pacman -S mingw-w64-ucrt-x86_64-qemu in a UCRT64 shell first"
	}
	return "no QEMU yet; pvm needs one to run VMs. Choose one, once: pvm qemu install (a QEMU built by the pvm-qemu project) or pvm qemu use official (" + second + ")"
}

// Resolve returns the default build, checked. It reads qemu.json without
// the lock and never searches PATH, migrates or switches builds.
func Resolve(p *paths.Paths) (*Toolchain, error) {
	st, err := LoadState(p)
	if err != nil {
		return nil, &UnusableError{Msg: err.Error()}
	}
	if st.Default == "" {
		if c := AdoptionCandidates(p); len(c) > 0 {
			return nil, &UnusableError{Msg: fmt.Sprintf("%s holds a QEMU from an older pvm that has not been taken over yet; restart the daemon (pvm daemon restart) or run pvm qemu ls", c[0].Dir)}
		}
		return nil, ErrNotSetUp
	}
	return ResolveBuild(p, st, st.Default)
}

// ResolveBuild checks one registered build and returns its toolchain.
// A project build is verified file by file once per process and by stat
// at every call; one whose files changed is refused. An official build
// whose binary changed is checked again before use.
func ResolveBuild(p *paths.Paths, st *State, id string) (*Toolchain, error) {
	b := st.Find(id)
	if b == nil {
		return nil, &UnusableError{Msg: fmt.Sprintf("QEMU build %s is not registered; run pvm qemu ls", id)}
	}
	if b.Source == SourceProject {
		pins, err := LoadPins()
		if err != nil {
			return nil, &UnusableError{Msg: err.Error()}
		}
		if reason := pins.RevokedReason(b.ID); reason != "" {
			return nil, &UnusableError{Msg: fmt.Sprintf("%s was withdrawn by this pvm release (%s); install the recommended build: pvm qemu install --use", b.ID, reason)}
		}
		if err := checkTree(b.Dir); err != nil {
			return nil, &UnusableError{Msg: fmt.Sprintf("%s failed its check: %v; reinstall: pvm qemu rm %s -y && pvm qemu install %s --use", b.ID, err, b.ID, b.ID)}
		}
	} else {
		if _, err := os.Stat(b.System); err != nil {
			return nil, &UnusableError{Msg: fmt.Sprintf("the default QEMU (%s, %s) is no longer there; reinstall it or pick another: pvm qemu ls", ChannelLabel(b.Channel), b.System)}
		}
		changed, sha, err := officialChanged(b.SystemExec, b.SystemSHA256)
		if err != nil {
			return nil, &UnusableError{Msg: fmt.Sprintf("%s: %v", ChannelLabel(b.Channel), err)}
		}
		if changed {
			nb := *b
			old := nb.Version
			nb.SystemSHA256 = sha
			if err := Check(&nb); err != nil {
				return nil, &UnusableError{Msg: fmt.Sprintf("%s changed on disk (%s -> %s) and %v", ChannelLabel(b.Channel), old, nb.Version, err)}
			}
			nb.CheckedBy = buildinfo.Version
			log.Printf("qemu: %s changed on disk (%s -> %s) and passed its checks again", ChannelLabel(b.Channel), old, nb.Version)
			// Best effort: a failed save only means the next process checks again.
			_ = UpdateState(p, func(s *State) error {
				if cur := s.Find(nb.ID); cur != nil && cur.System == nb.System {
					s.Put(nb)
				}
				return nil
			})
			b = &nb
		}
	}
	if b.CheckedBy != buildinfo.Version {
		// A newer pvm may require more than the one that registered it.
		nb := *b
		if err := Check(&nb); err != nil {
			return nil, &UnusableError{Msg: err.Error()}
		}
		nb.CheckedBy = buildinfo.Version
		_ = UpdateState(p, func(s *State) error {
			if cur := s.Find(nb.ID); cur != nil && cur.SystemSHA256 == nb.SystemSHA256 {
				s.Put(nb)
			}
			return nil
		})
		b = &nb
	}
	v, _ := parseVersion("QEMU emulator version " + b.Version)
	id2 := b.BuildIdentity
	return &Toolchain{System: b.System, Img: b.Img, FirmwareDir: b.FirmwareDir, Version: v, Caps: b.Caps, Build: &id2}, nil
}

// Ready reports whether a default build is set or an older pvm's QEMU is
// waiting to be taken over. It does not check the build.
func Ready(p *paths.Paths) bool {
	st, err := LoadState(p)
	if err != nil {
		return false
	}
	return st.Default != "" || len(AdoptionCandidates(p)) > 0
}

func exeSuffix(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

// Verify checks a registered build without writing anything: what
// pvm qemu check --read-only runs, and what pvm upgrade asks the new
// pvm before it replaces the old one.
func Verify(p *paths.Paths, id string) error {
	st, err := LoadState(p)
	if err != nil {
		return &UnusableError{Msg: err.Error()}
	}
	if id == "" {
		id = st.Default
	}
	if id == "" {
		return ErrNotSetUp
	}
	b := st.Find(id)
	if b == nil {
		return &UnusableError{Msg: fmt.Sprintf("QEMU build %s is not registered; run pvm qemu ls", id)}
	}
	nb := *b
	if b.Source == SourceProject {
		pins, err := LoadPins()
		if err != nil {
			return err
		}
		if reason := pins.RevokedReason(b.ID); reason != "" {
			return &UnusableError{Msg: fmt.Sprintf("%s was withdrawn by this pvm release (%s); install the recommended build: pvm qemu install --use", b.ID, reason)}
		}
		if _, err := VerifyTree(b.Dir); err != nil {
			return &UnusableError{Msg: fmt.Sprintf("%s failed its check: %v; reinstall: pvm qemu rm %s -y && pvm qemu install %s --use", b.ID, err, b.ID, b.ID)}
		}
	} else if sha, err := fileSHA256(b.SystemExec); err != nil {
		return &UnusableError{Msg: fmt.Sprintf("%s: %v", ChannelLabel(b.Channel), err)}
	} else {
		nb.SystemSHA256 = sha
	}
	if err := Check(&nb); err != nil {
		return &UnusableError{Msg: err.Error()}
	}
	return nil
}
