package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huangzheng2016/pvm/internal/paths"
)

// Remove unregisters a build and, for a project build, deletes its files.
// The default cannot be removed, and neither can a build a running QEMU
// executes. An official build is only unregistered: its channel owns it.
func Remove(p *paths.Paths, id string, inUse func(dir string) bool) error {
	var trash string
	err := LockState(p, func() error {
		st, err := LoadState(p)
		if err != nil {
			return err
		}
		b := st.Find(id)
		if b == nil {
			return fmt.Errorf("QEMU build %s is not registered; see pvm qemu ls", id)
		}
		// A default whose files changed can go: that is how it is
		// reinstalled. Any other default stays until another is picked.
		if st.Default == id && (b.Source != SourceProject || checkTree(b.Dir) == nil) {
			return fmt.Errorf("%s is the default; pick another first: pvm qemu use <build>|official", id)
		}
		if b.Source == SourceProject {
			if inUse != nil && inUse(b.Dir) {
				return fmt.Errorf("a VM is running on %s; stop it first", id)
			}
			top := filepath.Dir(b.Dir)
			trash = filepath.Join(p.QEMUBuilds(), ".trash-"+id)
			os.RemoveAll(trash)
			if err := os.Rename(top, trash); err != nil {
				return fmt.Errorf("remove %s: %w (is a QEMU still running from it?)", top, err)
			}
		}
		st.Remove(id)
		if err := st.Save(p); err != nil {
			if trash != "" {
				os.Rename(trash, filepath.Dir(b.Dir))
				trash = ""
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if trash != "" {
		return os.RemoveAll(trash)
	}
	return nil
}

// Leftover is something pvm qemu gc can delete.
type Leftover struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Size   int64  `json:"size"`
}

// Leftovers lists what no registered build or recovery point uses: staging
// and trash folders, and older runtime folders that cannot be taken over
// (repacks of Homebrew, apt or weilnetz builds). keep reports paths a
// pvm upgrade recovery point still needs.
func Leftovers(p *paths.Paths, keep func(path string) bool) []Leftover {
	var out []Leftover
	add := func(path, reason string) {
		if keep != nil && keep(path) {
			return
		}
		out = append(out, Leftover{Path: path, Reason: reason, Size: dirSize(path)})
	}
	if entries, err := os.ReadDir(p.QEMUBuilds()); err == nil {
		st, _ := LoadState(p)
		for _, e := range entries {
			path := filepath.Join(p.QEMUBuilds(), e.Name())
			switch {
			case strings.HasPrefix(e.Name(), ".stage-"), strings.HasPrefix(e.Name(), ".trash-"):
				add(path, "an interrupted install or removal")
			case st != nil && st.Find(e.Name()) == nil:
				add(path, "not registered in qemu.json")
			}
		}
	}
	root := filepath.Join(p.Runtime, "qemu")
	for _, c := range LegacyTrees(p) {
		if c.Tag == "" {
			add(c.Dir, "the QEMU an older pvm shipped (not a pvm-qemu build)")
		}
	}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".upgrade-qemu-") || strings.HasPrefix(n, ".pvmq-validate.") || strings.Contains(n, ".previous-") || strings.HasPrefix(n, "release-") || strings.HasSuffix(n, ".new") || strings.HasSuffix(n, ".old") || strings.HasSuffix(n, ".pre-pvm-qemu") {
				add(filepath.Join(root, n), "a backup or staging folder of an older pvm")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func dirSize(path string) int64 {
	var n int64
	filepath.Walk(path, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

// InUse reports whether a running VM executes QEMU from dir. It errs on
// the side of "in use" when a VM's state cannot be read.
func InUse(p *paths.Paths, dir string) bool {
	entries, err := os.ReadDir(p.VMs)
	if err != nil {
		return !os.IsNotExist(err)
	}
	prefix := strings.ToLower(filepath.Clean(dir) + string(filepath.Separator))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inst := &Instance{StatePath: filepath.Join(p.VMs, e.Name(), "run.json")}
		st, err := inst.Running()
		if err != nil {
			return true
		}
		if st != nil && len(st.Args) > 0 && strings.HasPrefix(strings.ToLower(filepath.Clean(st.Args[0])), prefix) {
			return true
		}
	}
	return false
}
