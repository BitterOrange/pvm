//go:build !windows

package qemu

import (
	"errors"
	"fmt"
	"syscall"
)

// killVerified re-checks the recorded identity immediately before
// signalling, so a recycled PID is never signalled by mistake.
func killVerified(st *RunState, sig syscall.Signal) error {
	if st.Ident == nil || st.PID <= 0 || st.PID != st.Ident.PID {
		return fmt.Errorf("refusing to signal invalid process identity")
	}
	cur, err := IdentityOf(st.PID)
	if errors.Is(err, errNoProc) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot verify process %d before signalling: %w", st.PID, err)
	}
	if !sameProcess(cur, st.Ident) {
		return nil
	}
	if err := syscall.Kill(st.PID, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill(%d, %v): %w", st.PID, sig, err)
	}
	return nil
}

func terminateGraceful(st *RunState) error {
	return killVerified(st, syscall.SIGTERM)
}

func terminateForce(st *RunState) error {
	return killVerified(st, syscall.SIGKILL)
}
