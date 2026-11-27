package daemon

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huangzheng2016/pvm/internal/vm"
)

func TestResizeSetsCPUsAndMemory(t *testing.T) {
	s, spec, _ := newMountTestServer(t)
	spec.CPUs, spec.MemMB, spec.DiskGB = 2, 4096, 20
	if err := spec.Save(s.P); err != nil {
		t.Fatal(err)
	}
	s.cacheSpec(spec)
	resize := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("POST", "/v1/vms/dev1/resize", bytes.NewBufferString(body)))
		return rr
	}
	saved := func() *vm.Spec {
		t.Helper()
		loaded, err := vm.Load(s.P, spec.Name)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}

	oldHost := hostMemMB
	hostMemMB = func() int { return 16384 }
	defer func() { hostMemMB = oldHost }()
	for body, want := range map[string]string{
		`{}`:                          "nothing to change",
		`{"cpus":65}`:                 "cpus 65 out of range",
		`{"cpus":-1}`:                 "cpus -1 out of range",
		`{"mem_mb":64}`:               "mem_mb 64 out of range",
		`{"cpus":4,"mem_mb":2097152}`: "mem_mb 2097152 out of range",
	} {
		if rr := resize(body); rr.Code != 400 || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("%s: %d %s, want 400 with %q", body, rr.Code, rr.Body.String(), want)
		}
	}
	if got := saved(); got.CPUs != 2 || got.MemMB != 4096 {
		t.Fatalf("a refused request changed the spec: %d cpus, %d MB", got.CPUs, got.MemMB)
	}

	// no disk size: qemu-img is not needed, and the test server has none
	if rr := resize(`{"cpus":4}`); rr.Code != 200 {
		t.Fatalf("cpus: %d %s", rr.Code, rr.Body.String())
	}
	if got := saved(); got.CPUs != 4 || got.MemMB != 4096 || got.DiskGB != 20 {
		t.Fatalf("after cpus: %d cpus, %d MB, %dG", got.CPUs, got.MemMB, got.DiskGB)
	}
	rr := resize(`{"cpus":6,"mem_mb":8192}`)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "note") {
		t.Fatalf("cpus and memory: %d %s", rr.Code, rr.Body.String())
	}
	if got := saved(); got.CPUs != 6 || got.MemMB != 8192 {
		t.Fatalf("after cpus and memory: %d cpus, %d MB", got.CPUs, got.MemMB)
	}
	if cached, err := s.getSpec(spec.Name); err != nil || cached.CPUs != 6 || cached.MemMB != 8192 {
		t.Fatalf("the daemon still starts the vm with %+v (%v)", cached, err)
	}
}

// A RAM snapshot restores only into the machine it was taken from.
func TestResizeKeepsSnapshotMachine(t *testing.T) {
	s, spec, _ := newMountTestServer(t)
	spec.CPUs, spec.MemMB = 2, 4096
	if err := spec.Save(s.P); err != nil {
		t.Fatal(err)
	}
	s.cacheSpec(spec)
	manifest := filepath.Join(s.P.VMDir(spec.Name), "snapshot.json")
	resize := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.handler().ServeHTTP(rr, httptest.NewRequest("POST", "/v1/vms/dev1/resize", bytes.NewBufferString(body)))
		return rr
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"dev1","cpus":2,"mem_mb":4096,"consumed":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"cpus":4}`, `{"mem_mb":8192}`} {
		rr := resize(body)
		if rr.Code != 409 || !strings.Contains(rr.Body.String(), "pvm snapshot rm dev1") {
			t.Errorf("%s with a snapshot: %d %s", body, rr.Code, rr.Body.String())
		}
	}
	if loaded, err := vm.Load(s.P, spec.Name); err != nil || loaded.CPUs != 2 || loaded.MemMB != 4096 {
		t.Fatalf("a refused request changed the spec: %+v (%v)", loaded, err)
	}
	// the same machine is no change
	if rr := resize(`{"cpus":2,"mem_mb":4096}`); rr.Code != 200 {
		t.Errorf("unchanged machine with a snapshot: %d %s", rr.Code, rr.Body.String())
	}
	// a restored snapshot is not restored again
	if err := os.WriteFile(manifest, []byte(`{"name":"dev1","cpus":2,"mem_mb":4096,"consumed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rr := resize(`{"cpus":4}`); rr.Code != 200 {
		t.Errorf("after the snapshot was restored: %d %s", rr.Code, rr.Body.String())
	}
}
