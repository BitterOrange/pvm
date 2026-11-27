package qemu

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/huangzheng2016/pvm/internal/paths"
)

const testTag = "11.1.3-pvm1"

// writeTree creates a minimal project build tree for this host at dir.
func writeTree(t *testing.T, dir, tag string) {
	t.Helper()
	sys := "bin/" + exeSuffix("qemu-system-"+QemuArch())
	files := map[string]string{
		sys:                            "fake emulator",
		"bin/" + exeSuffix("qemu-img"): "fake img",
		"share/firmware.fd":            "fw",
		"VERSION":                      tag + "\n",
	}
	m, _ := json.Marshal(manifest{Build: tag, QEMUVersion: "11.1.3", Host: HostKey(), System: sys, SystemExec: sys, Img: "bin/" + exeSuffix("qemu-img"), FirmwareDir: "share"})
	files["pvm-qemu.json"] = string(m)
	var sums []string
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		sums = append(sums, hexSum([]byte(body))+"  "+rel)
	}
	sort.Strings(sums)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeArchive packs tree as <arch>/... into a .tar.gz.
func writeArchive(t *testing.T, tree, out string, extra ...*tar.Header) {
	t.Helper()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(tree, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tree, path)
		name := QemuArch() + "/" + filepath.ToSlash(rel)
		if fi.IsDir() {
			return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755})
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

// usePins replaces the embedded pins for one test.
func usePins(t *testing.T, p *Pins) {
	t.Helper()
	LoadPins()
	old, oldErr := pinsVal, pinsErr
	pinsVal, pinsErr = p, nil
	t.Cleanup(func() { pinsVal, pinsErr = old, oldErr })
}

// pinFor pins the tree and archive at the given paths for this host.
func pinFor(t *testing.T, tag, tree, archive string) *Pins {
	t.Helper()
	ts, err := treeSum(tree)
	if err != nil {
		t.Fatal(err)
	}
	h := PinHost{Asset: fmt.Sprintf("pvm-qemu-%s-%s.tar.gz", tag, HostKey()), TreeSHA256: ts, Size: 1}
	if archive != "" {
		h.SHA256, _ = fileSHA256(archive)
	}
	m, _ := readManifest(tree)
	h.SystemSHA256, _ = fileSHA256(filepath.Join(tree, filepath.FromSlash(m.SystemExec)))
	return &Pins{Schema: 1, Repo: PinRepo, Workflow: PinWorkflow,
		Builds: map[string]PinBuild{tag: {Hosts: map[string]PinHost{HostKey(): h}}}}
}

func TestEmbeddedPinsParse(t *testing.T) {
	if _, err := parsePins(pinsJSON); err != nil {
		t.Fatal(err)
	}
}

func TestParsePinsRejects(t *testing.T) {
	good := func() map[string]any {
		h := map[string]any{"asset": "pvm-qemu-" + testTag + "-linux-amd64.tar.gz", "sha256": strings.Repeat("a", 64), "size": 1, "tree_sha256": strings.Repeat("b", 64), "system_sha256": strings.Repeat("c", 64)}
		return map[string]any{"schema": 1, "repo": PinRepo, "workflow": PinWorkflow,
			"recommended": map[string]any{"linux-amd64": testTag},
			"builds":      map[string]any{testTag: map[string]any{"hosts": map[string]any{"linux-amd64": h}}},
			"revoked":     map[string]any{}}
	}
	enc := func(v map[string]any) []byte { b, _ := json.Marshal(v); return b }
	if _, err := parsePins(enc(good())); err != nil {
		t.Fatalf("good pins: %v", err)
	}
	cases := map[string]func(map[string]any){
		"unknown field": func(v map[string]any) { v["extra"] = 1 },
		"other repo":    func(v map[string]any) { v["repo"] = "someone/pvm-qemu" },
		"bad tag": func(v map[string]any) {
			v["builds"] = map[string]any{"latest": v["builds"].(map[string]any)[testTag]}
			v["recommended"] = map[string]any{}
		},
		"short sha": func(v map[string]any) {
			v["builds"].(map[string]any)[testTag].(map[string]any)["hosts"].(map[string]any)["linux-amd64"].(map[string]any)["sha256"] = "abc"
		},
		"wrong asset name": func(v map[string]any) {
			v["builds"].(map[string]any)[testTag].(map[string]any)["hosts"].(map[string]any)["linux-amd64"].(map[string]any)["asset"] = "x.tar.gz"
		},
		"recommended not pinned": func(v map[string]any) { v["recommended"] = map[string]any{"darwin-arm64": testTag} },
		"recommended revoked":    func(v map[string]any) { v["revoked"] = map[string]any{testTag: "bad"} },
	}
	for name, mutate := range cases {
		v := good()
		mutate(v)
		if _, err := parsePins(enc(v)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestExtractArchive(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	writeTree(t, tree, testTag)
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	writeArchive(t, tree, archive)
	dst := t.TempDir()
	if err := extractArchive(archive, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTree(filepath.Join(dst, QemuArch())); err != nil {
		t.Fatalf("extracted tree: %v", err)
	}
}

func TestExtractArchiveRejects(t *testing.T) {
	cases := map[string]*tar.Header{
		"traversal": {Name: "../evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"absolute":  {Name: "/evil", Typeflag: tar.TypeReg, Mode: 0o644},
		"symlink":   {Name: QemuArch() + "/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		"duplicate": {Name: QemuArch() + "/VERSION", Typeflag: tar.TypeReg, Mode: 0o644},
	}
	for name, h := range cases {
		tree := filepath.Join(t.TempDir(), "tree")
		writeTree(t, tree, testTag)
		archive := filepath.Join(t.TempDir(), "a.tar.gz")
		writeArchive(t, tree, archive, h)
		parent := t.TempDir()
		dst := filepath.Join(parent, "dst")
		os.Mkdir(dst, 0o755)
		if err := extractArchive(archive, dst); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
			t.Errorf("%s: wrote outside the destination", name)
		}
	}
}

func TestReadManifestRejectsEscapingPaths(t *testing.T) {
	for _, rel := range []string{"", "../bin/qemu", "/usr/bin/qemu"} {
		dir := t.TempDir()
		m, _ := json.Marshal(manifest{Build: testTag, Host: HostKey(), System: rel, SystemExec: "bin/q", Img: "bin/i", FirmwareDir: "share"})
		os.WriteFile(filepath.Join(dir, "pvm-qemu.json"), m, 0o644)
		if _, err := readManifest(dir); err == nil {
			t.Errorf("system %q: accepted", rel)
		}
	}
}

func TestVerifyTree(t *testing.T) {
	tree := t.TempDir()
	writeTree(t, tree, testTag)
	if _, err := VerifyTree(tree); err != nil {
		t.Fatal(err)
	}

	changed := t.TempDir()
	writeTree(t, changed, testTag)
	os.WriteFile(filepath.Join(changed, "VERSION"), []byte("other\n"), 0o644)
	if _, err := VerifyTree(changed); err == nil || !strings.Contains(err.Error(), "VERSION differs") {
		t.Errorf("changed file: %v", err)
	}

	extra := t.TempDir()
	writeTree(t, extra, testTag)
	os.WriteFile(filepath.Join(extra, "bin", "planted"), []byte("x"), 0o755)
	if _, err := VerifyTree(extra); err == nil || !strings.Contains(err.Error(), "not listed") {
		t.Errorf("unlisted file: %v", err)
	}

	missing := t.TempDir()
	writeTree(t, missing, testTag)
	os.Remove(filepath.Join(missing, "VERSION"))
	if _, err := VerifyTree(missing); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("missing file: %v", err)
	}

	unsafe := t.TempDir()
	os.WriteFile(filepath.Join(unsafe, "SHA256SUMS"), []byte(strings.Repeat("a", 64)+"  ../x\n"), 0o644)
	if _, err := VerifyTree(unsafe); err == nil {
		t.Error("unsafe SHA256SUMS path accepted")
	}
}

func TestCheckTreeNoticesChanges(t *testing.T) {
	tree := t.TempDir()
	writeTree(t, tree, testTag)
	if err := checkTree(tree); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tree, "VERSION")
	os.WriteFile(path, []byte("tampered\n"), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(path, future, future)
	if err := checkTree(tree); err == nil {
		t.Fatal("a changed file passed the cached check")
	}
}

func TestStateRoundTripAndStrictness(t *testing.T) {
	p := paths.New(t.TempDir())
	st, err := LoadState(p)
	if err != nil || len(st.Builds) != 0 {
		t.Fatalf("missing file: %v %v", st, err)
	}
	if err := UpdateState(p, func(s *State) error {
		s.Put(Build{BuildIdentity: BuildIdentity{ID: testTag, Source: SourceProject}})
		s.Put(Build{BuildIdentity: BuildIdentity{ID: OfficialID, Source: SourceOfficial}})
		s.Default = testTag
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err = LoadState(p)
	if err != nil || st.Default != testTag || len(st.Builds) != 2 {
		t.Fatalf("reload: %+v %v", st, err)
	}
	st.Remove(testTag)
	if st.Default != "" || st.Find(testTag) != nil {
		t.Fatalf("remove: %+v", st)
	}

	for name, body := range map[string]string{
		"corrupt":         "{",
		"unknown field":   `{"schema":1,"default":"","builds":[],"extra":1}`,
		"other schema":    `{"schema":2,"default":"","builds":[]}`,
		"default missing": `{"schema":1,"default":"` + testTag + `","builds":[]}`,
	} {
		os.WriteFile(p.QEMUState(), []byte(body), 0o600)
		if _, err := LoadState(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if data, _ := os.ReadFile(p.QEMUState()); string(data) != body {
			t.Errorf("%s: the file was rewritten", name)
		}
	}
}

func TestRemoveBuild(t *testing.T) {
	p := paths.New(t.TempDir())
	dir := filepath.Join(p.QEMUBuilds(), testTag, QemuArch())
	writeTree(t, dir, testTag)
	other := "11.1.4-pvm1"
	otherDir := filepath.Join(p.QEMUBuilds(), other, QemuArch())
	writeTree(t, otherDir, other)
	UpdateState(p, func(s *State) error {
		s.Put(Build{BuildIdentity: BuildIdentity{ID: testTag, Source: SourceProject}, Dir: dir})
		s.Put(Build{BuildIdentity: BuildIdentity{ID: other, Source: SourceProject}, Dir: otherDir})
		s.Default = other
		return nil
	})
	if err := Remove(p, other, nil); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("removing the default: %v", err)
	}
	if err := Remove(p, testTag, func(string) bool { return true }); err == nil || !strings.Contains(err.Error(), "running") {
		t.Errorf("removing a build in use: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused removal deleted files: %v", err)
	}
	if err := Remove(p, testTag, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !os.IsNotExist(err) {
		t.Errorf("files remain: %v", err)
	}
	if st, _ := LoadState(p); st.Find(testTag) != nil {
		t.Error("still registered")
	}
}

func TestLeftovers(t *testing.T) {
	usePins(t, &Pins{Schema: 1, Repo: PinRepo, Workflow: PinWorkflow})
	p := paths.New(t.TempDir())
	root := filepath.Join(p.Runtime, "qemu")
	registered := filepath.Join(p.QEMUBuilds(), testTag, QemuArch())
	writeTree(t, registered, testTag)
	UpdateState(p, func(s *State) error {
		s.Put(Build{BuildIdentity: BuildIdentity{ID: testTag, Source: SourceProject}, Dir: registered})
		return nil
	})
	for _, d := range []string{
		filepath.Join(p.QEMUBuilds(), ".stage-x"),
		filepath.Join(p.QEMUBuilds(), "9.0.0-pvm1"),
		filepath.Join(root, QemuArch()), // a repack: no pinned SHA256SUMS
		filepath.Join(root, ".upgrade-qemu-123"),
		filepath.Join(root, "kept.previous-1"),
	} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644)
	}
	keep := filepath.Join(root, "kept.previous-1")
	got := map[string]bool{}
	for _, l := range Leftovers(p, func(path string) bool { return path == keep }) {
		got[l.Path] = true
	}
	for _, want := range []string{
		filepath.Join(p.QEMUBuilds(), ".stage-x"),
		filepath.Join(p.QEMUBuilds(), "9.0.0-pvm1"),
		filepath.Join(root, QemuArch()),
		filepath.Join(root, ".upgrade-qemu-123"),
	} {
		if !got[want] {
			t.Errorf("missing leftover %s", want)
		}
	}
	if got[keep] || got[filepath.Join(p.QEMUBuilds(), testTag)] || len(got) != 4 {
		t.Errorf("leftovers: %v", got)
	}
}

func TestLegacyAdoption(t *testing.T) {
	p := paths.New(t.TempDir())
	slot := filepath.Join(p.Runtime, "qemu", QemuArch())
	writeTree(t, slot, testTag)
	usePins(t, pinFor(t, testTag, slot, ""))

	c := AdoptionCandidates(p)
	if len(c) != 1 || c[0].Tag != testTag || c[0].Dir != slot {
		t.Fatalf("candidates: %+v", c)
	}
	errs := Adopt(p, func(string) bool { return true })
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "still runs") {
		t.Fatalf("adopting a folder in use: %v", errs)
	}
	if _, err := os.Stat(slot); err != nil {
		t.Fatal("a refused takeover moved the folder")
	}

	// A tampered copy is never taken over.
	os.WriteFile(filepath.Join(slot, "VERSION"), []byte("x\n"), 0o644)
	if errs := Adopt(p, nil); len(errs) != 1 || !strings.Contains(errs[0].Error(), "differs") {
		t.Fatalf("adopting a tampered folder: %v", errs)
	}
	os.WriteFile(filepath.Join(slot, "VERSION"), []byte(testTag+"\n"), 0o755)

	// An installed build makes the older copy a duplicate, which goes.
	installed := filepath.Join(p.QEMUBuilds(), testTag, QemuArch())
	writeTree(t, installed, testTag)
	UpdateState(p, func(s *State) error {
		s.Put(Build{BuildIdentity: BuildIdentity{ID: testTag, Source: SourceProject}, Dir: installed})
		s.Default = testTag
		return nil
	})
	if errs := Adopt(p, nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := os.Stat(slot); !os.IsNotExist(err) {
		t.Errorf("duplicate kept: %v", err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Errorf("installed build touched: %v", err)
	}
}

func TestInstallRefusals(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "tree")
	writeTree(t, tree, testTag)
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	writeArchive(t, tree, archive)
	ctx := context.Background()

	usePins(t, &Pins{Schema: 1, Repo: PinRepo, Workflow: PinWorkflow})
	p := paths.New(t.TempDir())
	if _, err := Install(ctx, p, InstallOptions{File: archive}); err == nil || !strings.Contains(err.Error(), "not an archive this pvm pins") {
		t.Errorf("unpinned file: %v", err)
	}
	if _, err := Install(ctx, p, InstallOptions{File: archive, Unpinned: true}); err == nil || !strings.Contains(err.Error(), "needs the build's tag") {
		t.Errorf("unpinned file without tag: %v", err)
	}
	if _, err := Install(ctx, p, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "pins no QEMU build") {
		t.Errorf("nothing recommended: %v", err)
	}
	if _, err := Install(ctx, p, InstallOptions{Tag: "latest"}); err == nil || !strings.Contains(err.Error(), "not a pvm-qemu tag") {
		t.Errorf("bad tag: %v", err)
	}
	if _, err := Install(ctx, p, InstallOptions{Tag: testTag}); err == nil || !strings.Contains(err.Error(), "not pinned") {
		t.Errorf("unpinned tag: %v", err)
	}

	pins := pinFor(t, testTag, tree, archive)
	usePins(t, pins)
	if _, err := Install(ctx, p, InstallOptions{File: archive, Tag: "11.1.4-pvm1"}); err == nil || !strings.Contains(err.Error(), "not 11.1.4-pvm1") {
		t.Errorf("file of another tag: %v", err)
	}
	pins.Revoked = map[string]string{testTag: "broken"}
	if _, err := Install(ctx, p, InstallOptions{File: archive}); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Errorf("revoked: %v", err)
	}
	pins.Revoked = nil

	// The archive matches its pin, but its tree does not: refused before
	// anything is registered or left behind.
	h := pins.Builds[testTag].Hosts[HostKey()]
	h.TreeSHA256 = strings.Repeat("0", 64)
	pins.Builds[testTag].Hosts[HostKey()] = h
	if _, err := Install(ctx, p, InstallOptions{File: archive}); err == nil || !strings.Contains(err.Error(), "SHA256SUMS does not match") {
		t.Errorf("tree pin mismatch: %v", err)
	}
	entries, _ := os.ReadDir(p.QEMUBuilds())
	if len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
	if st, _ := LoadState(p); len(st.Builds) != 0 {
		t.Errorf("registered: %+v", st.Builds)
	}
}

// A default whose files changed can be removed, which is how the error
// for it tells the user to reinstall it.
func TestRemoveBrokenDefault(t *testing.T) {
	p := paths.New(t.TempDir())
	dir := filepath.Join(p.QEMUBuilds(), testTag, QemuArch())
	writeTree(t, dir, testTag)
	UpdateState(p, func(s *State) error {
		s.Put(Build{BuildIdentity: BuildIdentity{ID: testTag, Source: SourceProject}, Dir: dir})
		s.Default = testTag
		return nil
	})
	if err := Remove(p, testTag, nil); err == nil {
		t.Fatal("removed an intact default")
	}
	os.WriteFile(filepath.Join(dir, "VERSION"), []byte("changed\n"), 0o644)
	future := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(dir, "VERSION"), future, future)
	if err := Remove(p, testTag, nil); err != nil {
		t.Fatal(err)
	}
	if st, _ := LoadState(p); st.Default != "" || len(st.Builds) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

// Debian and Ubuntu keep SeaBIOS outside /usr/share/qemu, so the firmware
// check looks through every directory the build searches.
func TestFirmwarePresentSearchesEveryDir(t *testing.T) {
	empty, seabios := t.TempDir(), t.TempDir()
	if firmwarePresent([]string{empty, seabios}, "bios-256k.bin") {
		t.Fatal("missing firmware reported present")
	}
	if err := os.WriteFile(filepath.Join(seabios, "bios-256k.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if firmwarePresent([]string{empty, seabios}, "bios-256k.bin") {
		t.Fatal("empty firmware reported present")
	}
	if err := os.WriteFile(filepath.Join(seabios, "bios-256k.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !firmwarePresent([]string{empty, seabios}, "bios-256k.bin") {
		t.Fatal("firmware in the second directory not found")
	}
}
