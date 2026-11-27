package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQEMUUpgradeRestoreWithoutPrevious(t *testing.T) {
	u := &qemuRuntimeUpgrade{current: filepath.Join(t.TempDir(), "x86_64")}
	if err := os.MkdirAll(u.current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := u.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(u.current); !os.IsNotExist(err) {
		t.Fatalf("new runtime was not removed: %v", err)
	}
}

func TestQEMUUpgradeRestoreIsIdempotent(t *testing.T) {
	root := t.TempDir()
	u := &qemuRuntimeUpgrade{
		current:         filepath.Join(root, "qemu", "x86_64"),
		backup:          filepath.Join(root, "qemu", "x86_64.previous"),
		hadPrevious:     true,
		previousVersion: "old",
	}
	if err := os.MkdirAll(u.current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.current, "VERSION"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := u.Restore(); err != nil {
		t.Fatalf("restored runtime should not require a second backup: %v", err)
	}
}

func TestQEMUUpgradeRestoreMissingBackupPreservesCurrent(t *testing.T) {
	root := t.TempDir()
	u := &qemuRuntimeUpgrade{
		current:         filepath.Join(root, "qemu", "x86_64"),
		backup:          filepath.Join(root, "qemu", "x86_64.previous"),
		hadPrevious:     true,
		previousVersion: "old",
	}
	if err := os.MkdirAll(u.current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.current, "VERSION"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := u.Restore(); err == nil {
		t.Fatal("missing backup must fail instead of deleting current runtime")
	}
	if _, err := os.Stat(filepath.Join(u.current, "VERSION")); err != nil {
		t.Fatalf("current runtime was removed: %v", err)
	}
}

func TestRestoreQEMUUpgradeUsesRecordedCurrent(t *testing.T) {
	root := t.TempDir()
	recorded := filepath.Join(root, "recorded", "x86_64")
	backup := recorded + ".previous"
	other := filepath.Join(root, "other", "x86_64")
	if err := os.MkdirAll(recorded, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "VERSION"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := upgradeRecovery{
		QEMUArch:        "x86_64",
		QEMUCurrent:     recorded,
		QEMUBackup:      backup,
		QEMUHadPrevious: true,
		QEMUVersion:     "old",
	}
	if err := restoreQEMUUpgrade(state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, "VERSION")); !os.IsNotExist(err) {
		t.Fatalf("unrelated state directory was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recorded, "VERSION")); err != nil {
		t.Fatalf("recorded runtime not restored: %v", err)
	}
}
