//go:build !windows && !linux

package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestKillVerifiedRefusesProbeFailure(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	id, err := IdentityOf(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\necho unavailable\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if err := killVerified(&RunState{PID: id.PID, Ident: id}, syscall.SIGTERM); err == nil {
		t.Fatal("must refuse signal when ps fails")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("process was signalled despite failed verification: %v", err)
	}
}
