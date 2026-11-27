package qemu

import "strings"

// ProcIdentity prevents a recycled PID from being signalled by mistake.
type ProcIdentity struct {
	PID     int    `json:"pid"`
	Started string `json:"started"` // ps lstart or creation timestamp, opaque but stable per boot+pid
	Comm    string `json:"comm"`
	State   string `json:"state"`
}

// Only a definitive not-found means dead; probe failures read as alive so
// callers never kill or double-start on uncertainty.
func (id *ProcIdentity) Alive() bool {
	cur, err := IdentityOf(id.PID)
	if err != nil {
		return err != errNoProc
	}
	if exitedZombie(cur.State) {
		return false
	}
	return sameProcess(cur, id)
}

// linuxStartPrefix marks a Linux start time read from /proc (boot id and
// clock ticks); other hosts record ps's wall-clock lstart.
const linuxStartPrefix = "linux:"

// sameProcess reports whether cur is the process rec was recorded from.
func sameProcess(cur, rec *ProcIdentity) bool {
	if cur.Comm != rec.Comm {
		return false
	}
	c, r := startedWithoutState(cur.Started), startedWithoutState(rec.Started)
	if c == r {
		return true
	}
	// A VM started by an older pvm on Linux holds a ps lstart that cannot
	// be compared with /proc ticks; like any probe uncertainty, the same
	// comm reads as the recorded process so it is never double-started.
	// v1.4.x only: deleted in v1.5.0, when no VM can still run from a
	// pre-/proc record.
	return strings.HasPrefix(c, linuxStartPrefix) && !strings.HasPrefix(r, linuxStartPrefix)
}

// exitedZombie reports a zombie that has fully exited. When QEMU exits with
// threads still blocked in disk io, Linux shows its main thread as a zombie
// ("Zl", l = multi-threaded) while those threads keep the disk image open
// and locked, so that process is still alive.
func exitedZombie(state string) bool {
	return strings.HasPrefix(state, "Z") && !strings.Contains(state, "l")
}

func startedWithoutState(started string) string {
	fields := strings.Fields(started)
	if len(fields) > 1 && len(fields[len(fields)-1]) <= 3 {
		return strings.Join(fields[:len(fields)-1], " ")
	}
	return started
}

type errT string

func (e errT) Error() string { return string(e) }

const errNoProc = errT("process not found")
