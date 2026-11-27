package qemu

import (
	"fmt"
	"path/filepath"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// Official is a QEMU that the platform's own channel installed, found at
// the channel's fixed location and owned by it. A binary that merely sits
// on PATH is never official.
type Official struct {
	Channel        string `json:"channel"` // homebrew, distro or msys2
	System         string `json:"system"`
	SystemExec     string `json:"system_exec"` // the real executable, after symlinks
	Img            string `json:"img"`
	FirmwareDir    string `json:"firmware_dir"`
	PackageVersion string `json:"package_version"` // the channel's package version, for diagnostics
}

// OfficialMissingError says the channel's QEMU is not installed, with the
// command that installs it. pvm never runs package managers itself.
type OfficialMissingError struct{ Msg string }

func (e *OfficialMissingError) Error() string { return e.Msg }

// UseOfficial registers the official QEMU as a build and, if setDefault,
// makes it the default. It checks the build before registering it.
func UseOfficial(p *paths.Paths, setDefault bool) (*Build, error) {
	o, err := DetectOfficial()
	if err != nil {
		return nil, err
	}
	sha, err := fileSHA256(o.SystemExec)
	if err != nil {
		return nil, err
	}
	b := Build{
		BuildIdentity: BuildIdentity{ID: OfficialID, Source: SourceOfficial, Channel: o.Channel, SystemSHA256: sha},
		System:        o.System, SystemExec: o.SystemExec, Img: o.Img, FirmwareDir: o.FirmwareDir,
	}
	if err := Check(&b); err != nil {
		return nil, err
	}
	b.CheckedBy = buildinfo.Version
	b.CheckedAt = nowUTC()
	err = UpdateState(p, func(s *State) error {
		s.Put(b)
		if setDefault || s.Default == "" {
			s.Default = b.ID
		}
		return nil
	})
	return &b, err
}

func officialMissing(format string, args ...any) error {
	return &OfficialMissingError{Msg: fmt.Sprintf(format, args...)}
}

func resolveLink(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
