package qemu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/huangzheng2016/pvm/internal/buildinfo"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// InstallOptions selects what pvm qemu install installs.
type InstallOptions struct {
	Tag      string // a pvm-qemu tag; empty means this pvm's recommended build
	File     string // install this archive instead of downloading it
	Use      bool   // make it the default even when one is set
	Unpinned bool   // allow a tag this pvm does not pin, checked with gh
	Progress func(done, total int64)
	Log      func(string)
}

// InstallResult says what Install did.
type InstallResult struct {
	Build            Build
	AlreadyInstalled bool
	MadeDefault      bool
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

// Install installs a project build. A pinned archive, downloaded or given
// with File, is accepted only when its SHA-256 equals this pvm's pin. A tag
// this pvm does not pin needs Unpinned and gh attestation verify. The two
// ways never fall back to each other. Download, extraction, verification
// and the checks run unlocked in builds/.stage-*; only the final rename
// and the qemu.json write hold run/qemu.lock.
func Install(ctx context.Context, p *paths.Paths, o InstallOptions) (*InstallResult, error) {
	logf := func(format string, args ...any) {
		if o.Log != nil {
			o.Log(fmt.Sprintf(format, args...))
		}
	}
	pins, err := LoadPins()
	if err != nil {
		return nil, err
	}
	builds := p.QEMUBuilds()
	if err := os.MkdirAll(builds, 0o700); err != nil {
		return nil, err
	}

	archive := o.File
	tag := o.Tag
	var pin PinHost
	pinned := false
	if archive != "" {
		sha, err := fileSHA256(archive)
		if err != nil {
			return nil, err
		}
		t, h, ok := pins.BySHA256(sha)
		switch {
		case ok && (tag == "" || tag == t):
			tag, pin, pinned = t, h, true
		case ok:
			return nil, fmt.Errorf("%s is the pinned archive of %s, not %s", archive, t, tag)
		case !o.Unpinned:
			return nil, fmt.Errorf("%s is not an archive this pvm pins for %s (sha256 %s); install a pinned build, or pass --unpinned with its tag to check it with gh", archive, HostKey(), sha)
		case tag == "":
			return nil, errors.New("--unpinned --file needs the build's tag")
		}
	} else {
		if tag == "" {
			tag = pins.RecommendedTag()
			if tag == "" {
				return nil, fmt.Errorf("this pvm pins no QEMU build for %s; use pvm qemu use official", HostKey())
			}
		}
		if !ValidTag(tag) {
			return nil, fmt.Errorf("%q is not a pvm-qemu tag such as 11.1.3-pvm1", tag)
		}
		pin, pinned = pins.Host(tag)
		if !pinned && !o.Unpinned {
			return nil, fmt.Errorf("%s is not pinned by this pvm; --unpinned checks its attestation with the GitHub CLI (gh) instead, or install the pinned build: pvm qemu install", tag)
		}
	}
	if reason := pins.RevokedReason(tag); reason != "" {
		return nil, fmt.Errorf("%s was withdrawn by this pvm release (%s); install the recommended build: pvm qemu install", tag, reason)
	}

	// Already installed and intact: only (maybe) make it the default.
	if st, err := LoadState(p); err != nil {
		return nil, err
	} else if b := st.Find(tag); b != nil {
		if err := checkTree(b.Dir); err == nil {
			res := &InstallResult{Build: *b, AlreadyInstalled: true}
			if o.Use || st.Default == "" {
				err := UpdateState(p, func(s *State) error { s.Default = tag; return nil })
				res.MadeDefault = err == nil
				return res, err
			}
			return res, nil
		}
		return nil, fmt.Errorf("%s is installed but its files changed; remove it first: pvm qemu rm %s -y", tag, tag)
	}

	asset := fmt.Sprintf("pvm-qemu-%s-%s.tar.gz", tag, HostKey())
	if archive == "" {
		if err := checkFreeSpace(builds, 3*pin.Size); err != nil {
			return nil, err
		}
		archive = filepath.Join(builds, ".stage-"+tag+".tar.gz")
		url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", PinRepo, tag, asset)
		logf("downloading %s", asset)
		if err := download(ctx, url, archive, pin.Size, o.Progress); err != nil {
			return nil, err
		}
		defer os.Remove(archive)
	}
	sha, err := fileSHA256(archive)
	if err != nil {
		return nil, err
	}
	if pinned {
		if sha != pin.SHA256 {
			return nil, fmt.Errorf("%s has sha256 %s, but this pvm pins %s for %s", asset, sha, pin.SHA256, tag)
		}
	} else if err := verifyAttestation(archive, tag); err != nil {
		return nil, err
	}

	stage, err := os.MkdirTemp(builds, ".stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	logf("verifying %s", asset)
	if err := extractArchive(archive, stage); err != nil {
		return nil, fmt.Errorf("extract %s: %w", asset, err)
	}
	tree := filepath.Join(stage, QemuArch())
	entries, _ := os.ReadDir(stage)
	if len(entries) != 1 || entries[0].Name() != QemuArch() {
		return nil, fmt.Errorf("%s does not hold exactly %s/", asset, QemuArch())
	}
	if _, err := VerifyTree(tree); err != nil {
		return nil, fmt.Errorf("%s: %w", asset, err)
	}
	if pinned {
		if ts, err := treeSum(tree); err != nil || ts != pin.TreeSHA256 {
			return nil, fmt.Errorf("%s: its SHA256SUMS does not match this pvm's pin", asset)
		}
	}
	m, err := readManifest(tree)
	if err != nil {
		return nil, err
	}
	if m.Build != tag || m.Host != HostKey() {
		return nil, fmt.Errorf("%s says it is %s for %s", asset, m.Build, m.Host)
	}
	final := filepath.Join(builds, tag)
	b := projectBuild(tree, m, sha, "")
	if b.SystemSHA256, err = fileSHA256(b.SystemExec); err != nil {
		return nil, err
	}
	if pinned && b.SystemSHA256 != pin.SystemSHA256 {
		return nil, fmt.Errorf("%s: its emulator does not match this pvm's pin", asset)
	}
	logf("checking %s", tag)
	if err := Check(&b); err != nil {
		return nil, err
	}
	res := &InstallResult{}
	err = LockState(p, func() error {
		st, err := LoadState(p)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(final); err == nil {
			return fmt.Errorf("%s already exists but is not registered; remove it with pvm qemu gc", final)
		}
		if err := renameRetry(stage, final); err != nil {
			return err
		}
		fb := projectBuild(filepath.Join(final, QemuArch()), m, sha, b.SystemSHA256)
		fb.Caps, fb.Version, fb.VersionLine = b.Caps, b.Version, b.VersionLine
		fb.CheckedBy, fb.CheckedAt = buildinfo.Version, nowUTC()
		st.Put(fb)
		if o.Use || st.Default == "" {
			st.Default = tag
			res.MadeDefault = true
		}
		if err := st.Save(p); err != nil {
			renameRetry(final, stage)
			return err
		}
		res.Build = fb
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// verifyAttestation checks an unpinned archive against pvm-qemu's release
// workflow for the tag. It needs the GitHub CLI.
func verifyAttestation(path, tag string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("%s is not pinned by this pvm; --unpinned needs the GitHub CLI (gh) to verify its attestation; install gh, or install the pinned build: pvm qemu install", tag)
	}
	cmd := exec.Command("gh", "attestation", "verify", path, "--repo", PinRepo,
		"--signer-workflow", PinWorkflow, "--source-ref", "refs/tags/"+tag,
		"--deny-self-hosted-runners", "--hostname", "github.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh attestation verify of %s failed: %s", filepath.Base(path), firstLine(string(out)))
	}
	return nil
}

// renameRetry renames, retrying on Windows while a scanner such as
// Defender still holds freshly written files.
func renameRetry(from, to string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
