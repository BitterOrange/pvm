package daemon

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/native"
	"github.com/huangzheng2016/pvm/internal/qemu"
	"github.com/huangzheng2016/pvm/internal/vm"
)

// qemuSelect caches the resolved default build. The daemon never resolves
// QEMU at startup and never exits for want of it: it resolves at each use,
// so `pvm qemu use|install` takes effect at the next start. A result is
// reused for two seconds unless qemu.json changes; detecting the
// accelerator happens once per binary.
type qemuSelect struct {
	mu     sync.Mutex
	at     time.Time
	stamp  string
	tc     *qemu.Toolchain
	accel  qemu.Accel
	err    error
	accels map[string]qemu.Accel
}

func (s *Server) stateStamp() string {
	fi, err := os.Stat(s.P.QEMUState())
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

// qemu returns the default build and its accelerator, or why there is none.
// A server built with a fixed toolchain (tests) always returns that one.
func (s *Server) qemu() (*qemu.Toolchain, qemu.Accel, error) {
	if s.TC != nil {
		return s.TC, s.Accel, nil
	}
	q := &s.qsel
	q.mu.Lock()
	defer q.mu.Unlock()
	stamp := s.stateStamp()
	if !q.at.IsZero() && stamp == q.stamp && time.Since(q.at) < 2*time.Second {
		return q.tc, q.accel, q.err
	}
	tc, err := qemu.Resolve(s.P)
	q.at, q.stamp, q.tc, q.err, q.accel = time.Now(), stamp, tc, err, ""
	if err != nil {
		return nil, "", err
	}
	if q.accels == nil {
		q.accels = map[string]qemu.Accel{}
	}
	key := tc.System + "@" + tc.Build.SystemSHA256
	accel, ok := q.accels[key]
	if !ok {
		accel = qemu.Detect(tc, runtime.GOOS)
		q.accels[key] = accel
		log.Printf("qemu: using %s (%s), accel %s", tc.Build.Label(), tc.System, accel)
	}
	q.accel = accel
	return tc, accel, nil
}

// qemuProblem is why VMs cannot start now, or "".
func (s *Server) qemuProblem() string {
	if _, _, err := s.qemu(); err != nil {
		return err.Error()
	}
	return ""
}

// qemuInUse reports whether a running VM executes QEMU from dir. Builds
// and older runtime folders are never moved or removed under a VM.
func (s *Server) qemuInUse(dir string) bool { return qemu.InUse(s.P, dir) }

// adoptLegacyQEMU takes over the QEMU an older pvm or the v1.4.0
// installers placed, when it is a pinned pvm-qemu build. It runs only in
// the daemon, which holds the run lock, so no older daemon still launches
// from the old folder.
func (s *Server) adoptLegacyQEMU() {
	if s.TC != nil || len(qemu.AdoptionCandidates(s.P)) == 0 {
		return
	}
	for _, err := range qemu.Adopt(s.P, s.qemuInUse) {
		log.Printf("qemu: %v", err)
	}
}

// watchQEMU notices a changed qemu.json or older runtime folder: it retries
// a pending takeover and the clusters blocked for want of QEMU.
func (s *Server) watchQEMU(stop <-chan struct{}) {
	last := s.qemuWatchStamp()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		cur := s.qemuWatchStamp()
		if cur == last {
			continue
		}
		last = cur
		s.adoptLegacyQEMU()
		if s.hasBlockedClusters() {
			log.Printf("qemu: selection changed; recovering blocked clusters")
			s.recoverClusters()
		}
	}
}

func (s *Server) qemuWatchStamp() string {
	stamp := s.stateStamp()
	for _, c := range qemu.LegacyTrees(s.P) {
		stamp += "|" + c.Dir + ":" + c.Tag
	}
	return stamp
}

// resetGatewayVars gives an arm64 gateway blank UEFI variables before it
// cold-boots. A gateway that once failed to boot (on an M1 Mac its 64 root
// ports used to exceed the 16 PCI buses QEMU's virt machine has there)
// keeps only the UEFI shell as its boot entry and drops into the shell on
// every later boot. Its variables hold nothing else worth keeping: the
// firmware boots the disk's default loader from blank variables.
func (s *Server) resetGatewayVars(spec *vm.Spec) {
	if spec.Role != "gateway" || native.Arch != "arm64" {
		return
	}
	path := spec.Vars(s.P)
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := fsutil.AtomicWrite(path, make([]byte, 64<<20), 0o600); err != nil {
		log.Printf("gateway %s: reset UEFI variables: %v", spec.Name, err)
	}
}
