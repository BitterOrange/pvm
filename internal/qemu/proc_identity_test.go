package qemu

import (
	"os"
	"testing"
)

func TestIdentityOfCurrentAndMissingProcess(t *testing.T) {
	id, err := IdentityOf(os.Getpid())
	if err != nil {
		t.Fatalf("IdentityOf(self): %v", err)
	}
	if !id.Alive() {
		t.Fatal("self reported not alive")
	}
	missing, err := IdentityOf(999999)
	if err != errNoProc {
		t.Fatalf("missing process error = %v, want errNoProc", err)
	}
	if missing != nil && missing.Alive() {
		t.Fatal("missing process reported alive")
	}
}

func TestExitedZombie(t *testing.T) {
	for state, want := range map[string]bool{
		"Z": true, "Z+": true, "Zs": true,
		"Zl": false, "Zl+": false, // main thread gone, io threads still hold the image
		"Ss": false, "Dl": false, "": false,
	} {
		if got := exitedZombie(state); got != want {
			t.Errorf("exitedZombie(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestSameProcess(t *testing.T) {
	rec := &ProcIdentity{Comm: "qemu-system-x86", Started: "linux:b00d:100"}
	for _, c := range []struct {
		cur  ProcIdentity
		want bool
	}{
		{ProcIdentity{Comm: "qemu-system-x86", Started: "linux:b00d:100"}, true},
		{ProcIdentity{Comm: "qemu-system-x86", Started: "linux:b00d:101"}, false},
		{ProcIdentity{Comm: "qemu-system-x86", Started: "linux:beef:100"}, false},
		{ProcIdentity{Comm: "sh", Started: "linux:b00d:100"}, false},
	} {
		if got := sameProcess(&c.cur, rec); got != c.want {
			t.Errorf("sameProcess(%+v) = %v, want %v", c.cur, got, c.want)
		}
	}
	// A record from an older pvm keeps ps's wall-clock start; on Linux it
	// cannot be compared, so the same comm counts as the same process.
	legacy := &ProcIdentity{Comm: "qemu-system-x86", Started: "Fri Nov 27 20:33:42 2026"}
	if !sameProcess(&ProcIdentity{Comm: "qemu-system-x86", Started: "linux:b00d:100"}, legacy) {
		t.Error("legacy record of a live process read as a different process")
	}
	if sameProcess(&ProcIdentity{Comm: "sh", Started: "linux:b00d:100"}, legacy) {
		t.Error("legacy record matched a different comm")
	}
	// Two wall-clock times only match exactly (macOS, whose lstart is fixed).
	if sameProcess(&ProcIdentity{Comm: "qemu-system-x86", Started: "Fri Nov 27 20:33:43 2026"}, legacy) {
		t.Error("different lstart matched")
	}
}
