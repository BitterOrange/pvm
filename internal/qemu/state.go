package qemu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// Caps are what a build was probed to support.
type Caps struct {
	Accels   []string `json:"accels"`
	Virtio9p bool     `json:"virtio_9p"`
}

// Build is one registered QEMU in qemu.json. Paths are absolute, so the
// daemon, the CLI and PVM Desktop run the same binary whatever their PATH.
type Build struct {
	BuildIdentity
	Dir           string    `json:"dir,omitempty"` // project builds: builds/<tag>/<arch>
	System        string    `json:"system"`        // what pvm runs
	SystemExec    string    `json:"system_exec"`   // the real executable (behind the Linux wrapper)
	Img           string    `json:"img"`
	FirmwareDir   string    `json:"firmware_dir"`
	ArchiveSHA256 string    `json:"archive_sha256,omitempty"`
	Caps          Caps      `json:"caps"`
	VersionLine   string    `json:"version_line"`
	CheckedBy     string    `json:"checked_by"` // the pvm version that last checked it
	CheckedAt     time.Time `json:"checked_at"`
}

// State is ~/.pvm/qemu.json: the registered builds and the default.
type State struct {
	Schema  int     `json:"schema"`
	Default string  `json:"default"`
	Builds  []Build `json:"builds"`
}

// LoadState reads qemu.json. A missing file is an empty state; a corrupt
// one is an error and is never reset, since resetting it would silently
// switch every VM to another build.
func LoadState(p *paths.Paths) (*State, error) {
	data, err := os.ReadFile(p.QEMUState())
	if errors.Is(err, os.ErrNotExist) {
		return &State{Schema: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s State
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s is unreadable (%v); fix or remove it, then run pvm qemu ls", p.QEMUState(), err)
	}
	if s.Schema != 1 {
		return nil, fmt.Errorf("%s has schema %d; this pvm reads schema 1", p.QEMUState(), s.Schema)
	}
	if s.Default != "" && s.Find(s.Default) == nil {
		return nil, fmt.Errorf("%s names default %q, which it does not register; run pvm qemu use <build>", p.QEMUState(), s.Default)
	}
	return &s, nil
}

// Find returns the registered build id, or nil.
func (s *State) Find(id string) *Build {
	for i := range s.Builds {
		if s.Builds[i].ID == id {
			return &s.Builds[i]
		}
	}
	return nil
}

// Put registers b, replacing a build with the same id.
func (s *State) Put(b Build) {
	for i := range s.Builds {
		if s.Builds[i].ID == b.ID {
			s.Builds[i] = b
			return
		}
	}
	s.Builds = append(s.Builds, b)
}

// Remove unregisters id.
func (s *State) Remove(id string) {
	out := s.Builds[:0]
	for _, b := range s.Builds {
		if b.ID != id {
			out = append(out, b)
		}
	}
	s.Builds = out
	if s.Default == id {
		s.Default = ""
	}
}

// Save writes qemu.json atomically. Call it only under LockState.
func (s *State) Save(p *paths.Paths) error {
	s.Schema = 1
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return err
	}
	// Go opens files on Windows without FILE_SHARE_DELETE, so a reader that
	// holds qemu.json open makes the rename fail for a moment.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = fsutil.AtomicWrite(p.QEMUState(), data, 0o600)
		if err == nil || runtime.GOOS != "windows" || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// LockState holds run/qemu.lock while fn changes builds or qemu.json. It
// covers only short critical sections: downloads, verification and probes
// happen before it is taken.
func LockState(p *paths.Paths, fn func() error) error {
	if err := os.MkdirAll(p.Run, 0o700); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		l, err := fsutil.TryLock(p.QEMULock())
		if err == nil {
			defer l.Unlock()
			return fn()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another pvm is changing QEMU builds; retry")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// UpdateState loads qemu.json under the lock, lets fn change it, and saves.
func UpdateState(p *paths.Paths, fn func(*State) error) error {
	return LockState(p, func() error {
		s, err := LoadState(p)
		if err != nil {
			return err
		}
		if err := fn(s); err != nil {
			return err
		}
		return s.Save(p)
	})
}
