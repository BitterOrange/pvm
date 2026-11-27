package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangzheng2016/pvm/internal/paths"
)

// The QEMU fields of an upgrade recovery point. pvm v1.3.x swapped its QEMU
// runtime folder during pvm upgrade; a v1.4.x pvm upgrade --rollback to a
// v1.3.x recovery point must put that folder back. v1.4.x only: v1.5.0
// deletes this file together with the other transitional code.

type qemuRuntimeUpgrade struct {
	arch            string
	stageRoot       string
	staged          string
	current         string
	backup          string
	hadPrevious     bool
	previousVersion string
}

func (u *qemuRuntimeUpgrade) Restore() error {
	if u == nil {
		return nil
	}
	if u.hadPrevious {
		if _, err := os.Lstat(u.backup); err != nil {
			if os.IsNotExist(err) {
				if u.previousVersion != "" {
					if data, readErr := os.ReadFile(filepath.Join(u.current, "VERSION")); readErr == nil && strings.TrimSpace(string(data)) == u.previousVersion {
						return nil
					}
				}
				return fmt.Errorf("QEMU rollback backup %s is missing; current runtime left unchanged", u.backup)
			}
			return err
		}
		if err := os.RemoveAll(u.current); err != nil {
			return err
		}
		return os.Rename(u.backup, u.current)
	}
	if _, err := os.Lstat(u.current); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.RemoveAll(u.current)
}

func restoreQEMUUpgrade(state upgradeRecovery) error {
	if state.QEMUArch == "" {
		return nil
	}
	current := state.QEMUCurrent
	if current == "" {
		p, err := paths.Default()
		if err != nil {
			return err
		}
		current = filepath.Join(p.Runtime, "qemu", state.QEMUArch)
	}
	u := &qemuRuntimeUpgrade{
		current:         current,
		backup:          state.QEMUBackup,
		hadPrevious:     state.QEMUHadPrevious,
		previousVersion: state.QEMUVersion,
	}
	return u.Restore()
}
