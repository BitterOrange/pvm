package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// MaybeNotifyOldDesktop tells Windows users once that the PVM Desktop next
// to pvm.exe predates v1.4.0. pvm upgrade replaces only pvm.exe, and such a
// PVM Desktop loses its QEMU when this pvm takes the old runtime folder
// over; the installer updates it. The release build strips the version
// from pvm-desktop.exe, so it is recognised by a marker string instead.
func MaybeNotifyOldDesktop() {
	if runtime.GOOS != "windows" || buildinfo.Version == "dev" {
		return
	}
	if !oldDesktopNextToPVM() {
		return
	}
	p, err := paths.Default()
	if err != nil {
		return
	}
	mark := filepath.Join(p.Home, "desktop-notice")
	if _, err := os.Stat(mark); err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "the PVM Desktop next to pvm.exe is older than pvm v1.4.0 and cannot set up QEMU, and pvm upgrade does not update it; rerun the pvm installer to get the new PVM Desktop")
	if os.MkdirAll(p.Home, 0o700) == nil {
		fsutil.AtomicWrite(mark, []byte(buildinfo.Version+"\n"), 0o600)
	}
}

// oldDesktopNextToPVM reports whether a pvm-desktop.exe without the marker
// sits next to this pvm.exe.
func oldDesktopNextToPVM() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "pvm-desktop.exe"))
	return err == nil && !bytes.Contains(data, []byte(buildinfo.DesktopQEMUMarker))
}
