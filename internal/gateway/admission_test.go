package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Until a lease is seen, every require-check VM counts as unverified, so
// the relays start closed; a valid lease lifts it and its expiry restores it.
func TestAdmissionUnverifiedVMs(t *testing.T) {
	c := &Config{
		VMs:          []VMEntry{{Name: "dev1", IP: "100.99.1.10"}, {Name: "dev2", IP: "100.99.1.11"}},
		RequireCheck: map[string]bool{"dev1": true},
	}
	var s admissionState
	if got := s.unverifiedVMs(c); !got["dev1"] || got["dev2"] {
		t.Fatalf("before any lease: %v", got)
	}
	now := time.Now()
	s.grants = map[string]time.Time{"dev1": now.Add(time.Minute)}
	if got := s.unverifiedLocked(c, now); got["dev1"] {
		t.Fatalf("with a valid lease: %v", got)
	}
	if got := s.unverifiedLocked(c, now.Add(2*time.Minute)); !got["dev1"] {
		t.Fatalf("after the lease expired: %v", got)
	}
}

// The daemon's wait script matches `"<sha256>"}` as success, so a receipt
// without an error must end right after the digest.
func TestAppliedReceiptShape(t *testing.T) {
	ok, err := json.Marshal(Applied{SHA256: "abc"})
	if err != nil || string(ok) != `{"sha256":"abc"}` {
		t.Fatalf("success receipt = %s, %v", ok, err)
	}
	failed, _ := json.Marshal(Applied{SHA256: "abc", Error: "rules: bad"})
	if strings.Contains(string(failed), `"abc"}`) {
		t.Fatalf("a failed receipt matches the success pattern: %s", failed)
	}
}
