//go:build !windows && !linux

package qemu

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func IdentityOf(pid int) (*ProcIdentity, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=,lstart=,stat=")
	// lstart is locale-sensitive (LC_TIME); CJK dates break the field-count parse.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(out)) == 0 {
			return nil, errNoProc
		}
		return nil, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, errNoProc
	}
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return nil, errNoProc
	}
	// comm has no spaces for qemu-system-*; lstart is the trailing 5 fields.
	comm := fields[0]
	started := strings.Join(fields[1:6], " ")
	state := ""
	if len(fields) > 6 {
		state = fields[6]
	}
	return &ProcIdentity{PID: pid, Started: started, Comm: comm, State: state}, nil
}
