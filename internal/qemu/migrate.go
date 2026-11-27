package qemu

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// The one-time takeover of the QEMU that pvm v1.3.x and the v1.4.0
// installers placed. It exists only in v1.4.x and is deleted in v1.5.0.
// Only a tree that is byte-identical to a pinned pvm-qemu build is taken
// over; a repack of Homebrew, apt or weilnetz is neither a project build
// nor an official one, so it is left untouched for pvm qemu gc.

// Candidate is a runtime folder that an older pvm or installer placed.
type Candidate struct {
	Dir  string `json:"dir"`            // the tree, after a macOS install.sh symlink
	Link string `json:"link,omitempty"` // the symlink that pointed at Dir, if any
	Tag  string `json:"tag,omitempty"`  // the pinned build its SHA256SUMS matches; empty for a repack
}

// legacySlots are the folders older pvm versions and installers used.
func legacySlots(p *paths.Paths) []string {
	root := filepath.Join(p.Runtime, "qemu")
	slots := []string{filepath.Join(root, QemuArch())}
	if runtime.GOOS == "windows" {
		slots = append(slots, filepath.Join(root, "msi-payload", QemuArch()))
	}
	return slots
}

// LegacyTrees lists the older runtime folders that exist, adoptable or not.
func LegacyTrees(p *paths.Paths) []Candidate {
	pins, err := LoadPins()
	if err != nil {
		return nil
	}
	root := filepath.Join(p.Runtime, "qemu")
	var out []Candidate
	for _, slot := range legacySlots(p) {
		fi, err := os.Lstat(slot)
		if err != nil {
			continue
		}
		c := Candidate{Dir: slot}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(slot)
			if err != nil {
				continue
			}
			rel, err := filepath.Rel(root, target)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue // a link out of runtime/qemu is never taken over
			}
			c.Link, c.Dir = slot, target
		}
		if sum, err := treeSum(c.Dir); err == nil {
			if tag, _, ok := pins.ByTree(sum); ok {
				c.Tag = tag
			}
		}
		out = append(out, c)
	}
	return out
}

// AdoptionCandidates lists the older runtime folders that can be taken
// over: their SHA256SUMS matches a pinned build.
func AdoptionCandidates(p *paths.Paths) []Candidate {
	var out []Candidate
	for _, c := range LegacyTrees(p) {
		if c.Tag != "" {
			out = append(out, c)
		}
	}
	return out
}

// Adopt takes over every adoptable older runtime folder: it verifies the
// files, renames the tree to builds/<tag>, registers it, and makes it the
// default when none is set. It must run only in the process holding the
// daemon run lock, so no older daemon still launches from the slot.
// inUse reports whether a running QEMU executes from a directory.
func Adopt(p *paths.Paths, inUse func(dir string) bool) []error {
	var errs []error
	for _, c := range AdoptionCandidates(p) {
		if err := adoptOne(p, c, inUse); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func adoptOne(p *paths.Paths, c Candidate, inUse func(string) bool) error {
	if inUse != nil && inUse(c.Dir) {
		return fmt.Errorf("%s holds pvm-qemu %s but a VM still runs from it; stop it and pvm takes it over", c.Dir, c.Tag)
	}
	if _, err := VerifyTree(c.Dir); err != nil {
		return fmt.Errorf("%s matches pinned %s but %v; install it instead: pvm qemu install --use", c.Dir, c.Tag, err)
	}
	m, err := readManifest(c.Dir)
	if err != nil {
		return err
	}
	if m.Build != c.Tag || m.Host != HostKey() {
		return fmt.Errorf("%s says it is %s for %s", c.Dir, m.Build, m.Host)
	}
	final := filepath.Join(p.QEMUBuilds(), c.Tag)
	return LockState(p, func() error {
		st, err := LoadState(p)
		if err != nil {
			return err
		}
		if b := st.Find(c.Tag); b != nil {
			// Already installed: the older copy is a byte-identical duplicate.
			if err := os.RemoveAll(c.Dir); err != nil {
				return err
			}
			if c.Link != "" {
				os.Remove(c.Link)
			}
			log.Printf("qemu: removed %s, a duplicate of installed %s", c.Dir, c.Tag)
			return nil
		}
		if err := os.MkdirAll(final, 0o700); err != nil {
			return err
		}
		dest := filepath.Join(final, QemuArch())
		if err := renameRetry(c.Dir, dest); err != nil {
			return fmt.Errorf("take over %s: %w", c.Dir, err)
		}
		if c.Link != "" {
			os.Remove(c.Link)
		}
		sys := filepath.Join(dest, filepath.FromSlash(m.SystemExec))
		sha, err := fileSHA256(sys)
		if err != nil {
			return err
		}
		b := projectBuild(dest, m, "", sha)
		b.Caps, err = Probe(b.System)
		if err != nil {
			renameRetry(dest, c.Dir)
			return err
		}
		// Check runs again at the next use, under the new pvm.
		b.CheckedBy = "adopted"
		b.CheckedAt = nowUTC()
		st.Put(b)
		if st.Default == "" {
			st.Default = b.ID
		}
		if err := st.Save(p); err != nil {
			renameRetry(dest, c.Dir)
			return err
		}
		log.Printf("qemu: took over %s as %s (pvm %s)", c.Dir, c.Tag, buildinfo.Version)
		return nil
	})
}
