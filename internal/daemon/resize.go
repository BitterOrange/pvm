package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"

	"github.com/huangzheng2016/pvm/internal/qemu"
	"github.com/huangzheng2016/pvm/internal/vm"
)

type resizeRequest struct {
	SizeGB int `json:"size_gb"`
	CPUs   int `json:"cpus"`
	MemMB  int `json:"mem_mb"`
}

// command is the line that asks for r again.
func (r resizeRequest) command(name string) string {
	line := "pvm resize " + name
	if r.SizeGB != 0 {
		line += fmt.Sprintf(" %d", r.SizeGB)
	}
	if r.CPUs != 0 {
		line += fmt.Sprintf(" --cpus %d", r.CPUs)
	}
	if r.MemMB != 0 {
		line += fmt.Sprintf(" --mem %d", r.MemMB)
	}
	return line
}

// handleResize sets what the request names: a zero field is left alone.
// The VM must be stopped: QEMU takes vcpus and memory at its start, and
// qemu-img cannot touch a live disk. The guest's cloud-init
// growpart/resizefs modules expand the partition and filesystem to the new
// ceiling automatically on the next boot.
func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	spec, err := s.getSpec(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	var req resizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	lock := qemu.LockVM(spec.Name)
	lock.Lock()
	defer lock.Unlock()
	spec, err = vm.Load(s.P, spec.Name)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if req == (resizeRequest{}) {
		writeErr(w, 400, fmt.Errorf("nothing to change: give a disk size, cpus or memory"))
		return
	}
	if req.SizeGB != 0 {
		if req.SizeGB < 1 || req.SizeGB > 4096 {
			writeErr(w, 400, fmt.Errorf("size must be between 1 and 4096 GiB"))
			return
		}
		if req.SizeGB <= spec.DiskGB {
			writeErr(w, 400, fmt.Errorf("size %dG must be larger than the current %dG; shrinking is not supported", req.SizeGB, spec.DiskGB))
			return
		}
	}
	if req.CPUs != 0 && (req.CPUs < 1 || req.CPUs > 64) {
		writeErr(w, 400, fmt.Errorf("cpus %d out of range [1,64]", req.CPUs))
		return
	}
	if req.MemMB != 0 && (req.MemMB < 128 || req.MemMB > 1048576) {
		writeErr(w, 400, fmt.Errorf("mem_mb %d out of range [128,1048576]", req.MemMB))
		return
	}
	if st, err := s.instance(spec).Running(); err != nil {
		writeErr(w, 500, err)
		return
	} else if st != nil {
		writeErr(w, 409, fmt.Errorf("vm %s is running; stop it first (`pvm stop %s`)", spec.Name, spec.Name))
		return
	}
	vmDir := s.P.VMDir(spec.Name)
	if (req.CPUs != 0 && req.CPUs != spec.CPUs) || (req.MemMB != 0 && req.MemMB != spec.MemMB) {
		// the next start would refuse to restore it into another machine
		manifest, err := qemu.LoadManifest(vmDir)
		if err != nil && !errors.Is(err, qemu.ErrNoSnapshot) {
			writeErr(w, 500, err)
			return
		}
		if manifest != nil && !manifest.Consumed {
			writeErr(w, 409, fmt.Errorf("vm %s has an unconsumed RAM snapshot of %d cpus and %d MB; restore it with `pvm start %s` or delete it with `pvm snapshot rm %s` before changing cpus or memory",
				spec.Name, manifest.CPUs, manifest.MemMB, spec.Name, spec.Name))
			return
		}
	}
	if req.SizeGB != 0 {
		tc, _, qerr := s.qemu()
		if qerr != nil {
			writeErr(w, 409, qerr)
			return
		}
		disk := qemu.ActiveDisk(vmDir, spec.Disk(s.P))
		out, err := exec.Command(tc.Img, "info", "--output=json", disk).CombinedOutput()
		if err != nil {
			writeErr(w, 500, fmt.Errorf("inspect active disk: %v\n%s", err, out))
			return
		}
		var info struct {
			VirtualSize int64 `json:"virtual-size"`
		}
		if err := json.Unmarshal(out, &info); err != nil || info.VirtualSize <= 0 {
			writeErr(w, 500, fmt.Errorf("invalid active disk size response"))
			return
		}
		requestedBytes := int64(req.SizeGB) << 30
		if requestedBytes < info.VirtualSize {
			writeErr(w, 400, fmt.Errorf("active disk is already larger than %dG; shrinking is not supported", req.SizeGB))
			return
		}
		if requestedBytes > info.VirtualSize {
			out, err = exec.Command(tc.Img, "resize", disk, fmt.Sprintf("%dG", req.SizeGB)).CombinedOutput()
			if err != nil {
				writeErr(w, 500, fmt.Errorf("qemu-img resize: %v\n%s", err, out))
				return
			}
		}
		spec.DiskGB = req.SizeGB
	}
	if req.CPUs != 0 {
		spec.CPUs = req.CPUs
	}
	if req.MemMB != 0 {
		spec.MemMB = req.MemMB
	}
	if err := spec.Save(s.P); err != nil {
		done := "spec save failed"
		if req.SizeGB != 0 {
			done = "disk resized but spec save failed"
		}
		writeErr(w, 500, fmt.Errorf("%s: %v; fix state storage and retry `%s` before starting the VM", done, err, req.command(spec.Name)))
		return
	}
	s.cacheSpec(spec)
	resp := map[string]any{"vm": spec.Name, "cpus": spec.CPUs, "mem_mb": spec.MemMB, "disk_gb": spec.DiskGB}
	if req.SizeGB != 0 {
		resp["note"] = "the guest grows its partition and filesystem to the new size automatically on the next boot (cloud-init)"
	}
	writeJSON(w, 200, resp)
}
