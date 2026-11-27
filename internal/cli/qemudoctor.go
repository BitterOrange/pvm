package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/paths"
	"github.com/huangzheng2016/pvm/internal/qemu"
)

// qemuDoctorChecks are doctor's QEMU checks. They write nothing, never
// start the daemon or use the network, and name no path.
func qemuDoctorChecks(p *paths.Paths) []auditCheck {
	label := func(detail string) string { return detail }
	if runtime.GOOS == "windows" {
		if backend, err := ResolveWindowsBackend(p); err == nil && backend != "native" {
			label = func(detail string) string { return "native (not used: backend is " + backend + "): " + detail }
		}
	}
	var checks []auditCheck
	add := func(name, status, detail string) { checks = append(checks, auditCheck{name, status, label(detail)}) }

	st, err := qemu.LoadState(p)
	if err != nil {
		add("qemu", "fail", "qemu.json is unreadable; fix or remove it, then run pvm qemu ls")
		return checks
	}
	pending := len(qemu.AdoptionCandidates(p)) > 0
	switch err := qemu.Verify(p, ""); {
	case err == nil:
		add("qemu", "ok", "the default build "+st.Default+" passed its checks")
	case errors.Is(err, qemu.ErrNotSetUp) && pending:
		add("qemu", "warn", "the QEMU an older pvm installed waits to be taken over at the daemon's next start")
	case errors.Is(err, qemu.ErrNotSetUp):
		add("qemu", "warn", "no QEMU build set up; run pvm qemu install, or pvm qemu use official")
	default:
		add("qemu", "fail", "the default build "+st.Default+" failed its checks; run pvm qemu check for the reason")
	}

	project, official := 0, 0
	for _, b := range st.Builds {
		if b.Source == qemu.SourceProject {
			project++
		} else {
			official++
		}
	}
	add("qemu_builds", "ok", fmt.Sprintf("%d project build(s), %d official", project, official))

	if pins, err := qemu.LoadPins(); err != nil {
		add("qemu_update", "fail", "the pins built into this pvm are unreadable")
	} else if b := st.Find(st.Default); b != nil && b.Source == qemu.SourceProject && pins.RevokedReason(b.ID) != "" {
		add("qemu_update", "fail", "the default build "+b.ID+" was withdrawn by this pvm release; run pvm qemu install --use")
	} else if rec := pins.RecommendedTag(); rec != "" && b != nil && b.Source == qemu.SourceProject && b.ID != rec {
		add("qemu_update", "warn", "this pvm recommends "+rec+"; to switch: pvm qemu install "+rec+" --use")
	} else {
		add("qemu_update", "ok", "no newer pinned build")
	}

	left := qemu.Leftovers(p, upgradeKeeps(p))
	old := preV14Snapshots(p)
	switch {
	case len(left) == 0 && len(old) == 0:
		add("qemu_leftovers", "ok", "nothing to clean up")
	default:
		var parts []string
		if len(left) > 0 {
			parts = append(parts, fmt.Sprintf("%d folder(s) pvm qemu gc can remove", len(left)))
		}
		if len(old) > 0 {
			parts = append(parts, fmt.Sprintf("%d RAM snapshot(s) from before v1.4.0 that cannot be restored; see pvm snapshot ls", len(old)))
		}
		add("qemu_leftovers", "warn", strings.Join(parts, "; "))
	}

	if runtime.GOOS == "windows" {
		if oldDesktopNextToPVM() {
			checks = append(checks, auditCheck{"qemu_desktop", "warn", "the PVM Desktop next to pvm.exe predates v1.4.0 and cannot set up QEMU; rerun the pvm installer"})
		} else {
			checks = append(checks, auditCheck{"qemu_desktop", "ok", "PVM Desktop can set up QEMU, or is not installed"})
		}
	}
	return checks
}

// preV14Snapshots names the VMs whose RAM snapshot a pvm before v1.4.0
// wrote: it records no QEMU build, so it cannot be restored.
func preV14Snapshots(p *paths.Paths) []string {
	entries, err := os.ReadDir(p.VMs)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := qemu.LoadManifest(filepath.Join(p.VMs, e.Name()))
		if err == nil && m.QEMUBuild == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// MaybeNotifyOldSnapshots lists, once, the VMs whose RAM snapshot pvm
// v1.4.0 and later cannot restore.
func MaybeNotifyOldSnapshots() {
	p, err := paths.Default()
	if err != nil {
		return
	}
	mark := filepath.Join(p.Home, "snapshot-notice")
	if _, err := os.Stat(mark); err == nil {
		return
	}
	if _, err := os.Stat(p.VMs); err != nil {
		return
	}
	if old := preV14Snapshots(p); len(old) > 0 {
		fmt.Fprintf(os.Stderr, "RAM snapshots written before pvm v1.4.0 cannot be restored: %s. To keep one, roll back (pvm upgrade --rollback -y), start the VM there, then upgrade again; or remove it: pvm snapshot rm <vm>\n", strings.Join(old, ", "))
	}
	if os.MkdirAll(p.Home, 0o700) == nil {
		fsutil.AtomicWrite(mark, []byte("1\n"), 0o600)
	}
}
