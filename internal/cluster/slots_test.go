package cluster

import (
	"fmt"
	"strings"
	"testing"
)

func TestAddVMStopsAtGatewayRootPorts(t *testing.T) {
	old := rootPorts
	rootPorts = func() int { return 15 }
	t.Cleanup(func() { rootPorts = old })
	s, err := LoadStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		if _, err := s.AddVM(1, fmt.Sprintf("dev%d", i)); err != nil {
			t.Fatalf("vm %d: %v", i, err)
		}
	}
	_, err = s.AddVM(1, "dev15")
	if err == nil || !strings.Contains(err.Error(), "15 root ports") {
		t.Fatalf("16th vm: %v", err)
	}
	if _, ok := s.Get(1).VMs["dev15"]; ok {
		t.Fatal("refused vm was recorded")
	}
}
