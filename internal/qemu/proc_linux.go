//go:build linux

package qemu

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// IdentityOf reads /proc rather than ps: ps derives lstart from the current
// wall clock minus uptime, so it shifts whenever the clock is stepped (NTP,
// WSL resyncing after host sleep) and a live QEMU stops matching its record.
// The boot id and the start time in clock ticks never change for a process.
func IdentityOf(pid int) (*ProcIdentity, error) {
	if pid <= 0 {
		return nil, errNoProc
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNoProc
	}
	if err != nil {
		return nil, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	return parseProcStat(pid, string(stat), strings.TrimSpace(string(boot)))
}

func parseProcStat(pid int, stat, bootID string) (*ProcIdentity, error) {
	open, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if open < 0 || end < open || bootID == "" {
		return nil, fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	// fields[0] is stat field 3 (state); num_threads is field 20 and
	// starttime field 22.
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return nil, fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	threads, err := strconv.Atoi(fields[17])
	if err != nil {
		return nil, fmt.Errorf("unparsable /proc/%d/stat: %w", pid, err)
	}
	state := fields[0]
	if threads > 1 {
		state += "l" // as ps reports it, for exitedZombie
	}
	return &ProcIdentity{
		PID: pid, Comm: stat[open+1 : end], State: state,
		Started: linuxStartPrefix + bootID + ":" + fields[19],
	}, nil
}
