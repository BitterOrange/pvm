//go:build linux

package qemu

import (
	"os"
	"strings"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	// comm may hold spaces and parentheses; only the last ')' ends it.
	stat := "4242 (qemu (x) 1) S 1 4242 4242 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 7 0 98765 1000 200 0 0"
	id, err := parseProcStat(4242, stat, "b00d")
	if err != nil {
		t.Fatal(err)
	}
	if id.Comm != "qemu (x) 1" || id.State != "Sl" || id.Started != "linux:b00d:98765" {
		t.Fatalf("got %+v", id)
	}
	// The started value is one field, so startedWithoutState keeps it whole
	// even when the tick count is short.
	short, err := parseProcStat(7, "7 (sh) Z 1 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 12 1 0 0 0", "b00d")
	if err != nil {
		t.Fatal(err)
	}
	if short.State != "Z" || !exitedZombie(short.State) || startedWithoutState(short.Started) != "linux:b00d:12" {
		t.Fatalf("got %+v", short)
	}
	if _, err := parseProcStat(1, "1 (init", "b00d"); err == nil {
		t.Fatal("truncated stat parsed")
	}
}

func TestIdentityOfSelfUsesProc(t *testing.T) {
	id, err := IdentityOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id.Started, linuxStartPrefix) {
		t.Fatalf("Started = %q, want a /proc identity", id.Started)
	}
	again, err := IdentityOf(os.Getpid())
	if err != nil || !sameProcess(again, id) {
		t.Fatalf("identity changed between reads: %+v vs %+v (%v)", again, id, err)
	}
}
