package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/huangzheng2016/pvm/internal/qemu"
)

// QEMUState is what the QEMU page and the Settings panel show. It is read
// without the daemon, so it also works on the WSL page, and never changes
// anything.
type QEMUState struct {
	// Ready is false until a default build is set (or an older pvm's QEMU
	// waits to be taken over): the app then shows its QEMU page.
	Ready           bool   `json:"ready"`
	PendingAdoption bool   `json:"pending_adoption"`
	Default         string `json:"default"`
	DefaultLabel    string `json:"default_label"`
	// Problem is why VMs cannot start now, as the daemon reports it.
	Problem string `json:"problem"`
	// Blocked names the clusters the daemon could not recover for want of
	// a usable QEMU.
	Blocked     []string        `json:"blocked"`
	Recommended QEMURecommended `json:"recommended"`
	Official    QEMUOfficial    `json:"official"`
}

type QEMURecommended struct {
	ID        string `json:"id"` // empty when this pvm pins none for the host
	SizeMB    int64  `json:"size_mb"`
	Installed bool   `json:"installed"`
}

type QEMUOfficial struct {
	Found   bool   `json:"found"`
	Label   string `json:"label"`
	Version string `json:"version"`
	Path    string `json:"path"`
	// Problem says how to install it when it is not found.
	Problem    string `json:"problem"`
	Registered bool   `json:"registered"`
}

// QEMUBuild is one registered build, for the Settings panel.
type QEMUBuild struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Source    string `json:"source"` // project or official
	Version   string `json:"version"`
	Path      string `json:"path"`
	Default   bool   `json:"default"`
	Virtio9p  bool   `json:"virtio_9p"`
	Withdrawn string `json:"withdrawn"`
}

// qemuEnv makes the CLI set up native QEMU even where the auto backend
// would pick WSL: the QEMU page is how a user moves the app to native.
var qemuEnv = []string{"PVM_BACKEND=native"}

func (a *App) readQEMU(status *daemonStatus) QEMUState {
	var q QEMUState
	q.Ready = qemu.Ready(a.p)
	q.PendingAdoption = len(qemu.AdoptionCandidates(a.p)) > 0
	st, err := qemu.LoadState(a.p)
	if err != nil {
		q.Problem = err.Error()
		st = &qemu.State{}
	}
	if b := st.Find(st.Default); b != nil {
		q.Default, q.DefaultLabel = b.ID, b.Label()
	}
	if pins, err := qemu.LoadPins(); err == nil {
		if tag := pins.RecommendedTag(); tag != "" {
			h, _ := pins.Host(tag)
			q.Recommended = QEMURecommended{ID: tag, SizeMB: (h.Size + 1<<20 - 1) >> 20, Installed: st.Find(tag) != nil}
		}
	}
	q.Official = a.official()
	q.Official.Registered = st.Find(qemu.OfficialID) != nil
	if status != nil {
		if len(status.QEMUProblems) > 0 && q.Problem == "" {
			q.Problem = status.QEMUProblems[0]
		}
		for _, b := range status.BlockedClusters {
			q.Blocked = append(q.Blocked, fmt.Sprintf("cluster %d: %s", b.Cluster, b.Reason))
		}
	}
	if q.Blocked == nil {
		q.Blocked = []string{}
	}
	return q
}

// official is detected once and again after CheckQEMUAgain: it reads the
// registry and the MSYS2 package database.
func (a *App) official() QEMUOfficial {
	a.officialMu.Lock()
	defer a.officialMu.Unlock()
	if a.officialCache == nil {
		var o QEMUOfficial
		if found, err := a.detectOfficial(); err != nil {
			o.Problem = err.Error()
		} else {
			o = QEMUOfficial{Found: true, Label: qemu.ChannelLabel(found.Channel), Version: found.PackageVersion, Path: found.System}
		}
		a.officialCache = &o
	}
	return *a.officialCache
}

// CheckQEMUAgain forgets the official QEMU detection, after the user
// installed it with pacman.
func (a *App) CheckQEMUAgain() error {
	a.officialMu.Lock()
	a.officialCache = nil
	a.officialMu.Unlock()
	return nil
}

// QEMUBuilds lists the registered builds.
func (a *App) QEMUBuilds() ([]QEMUBuild, error) {
	st, err := qemu.LoadState(a.p)
	if err != nil {
		return nil, err
	}
	pins, err := qemu.LoadPins()
	if err != nil {
		return nil, err
	}
	out := []QEMUBuild{}
	for _, b := range st.Builds {
		qb := QEMUBuild{ID: b.ID, Label: b.Label(), Source: b.Source, Version: b.Version, Path: b.System, Default: b.ID == st.Default, Virtio9p: b.Caps.Virtio9p}
		if b.Source == qemu.SourceProject {
			qb.Withdrawn = pins.RevokedReason(b.ID)
		}
		out = append(out, qb)
	}
	return out, nil
}

// qemuTask runs a pvm qemu command and lets the backend be resolved
// again afterwards: with a default set, auto picks native.
func (a *App) qemuTask(title string, argv ...string) Task {
	t := a.runTask(title, []step{{argv: argv, display: "PVM_BACKEND=native " + commandLine(argv...), env: qemuEnv}})
	a.backendMu.Lock()
	a.backendName = ""
	a.backendMu.Unlock()
	return t
}

// InstallQEMU installs the build this pvm recommends and makes it the
// default.
func (a *App) InstallQEMU() (Task, error) {
	return a.qemuTask("Install QEMU", "qemu", "install", "--use", "-y"), nil
}

// InstallQEMUFromFile installs a downloaded pvm-qemu archive; pvm accepts
// it only when its SHA-256 is one it pins.
func (a *App) InstallQEMUFromFile(path string) (Task, error) {
	if !filepath.IsAbs(path) || !strings.HasSuffix(strings.ToLower(path), ".tar.gz") {
		return Task{}, fmt.Errorf("%q is not a pvm-qemu .tar.gz archive", path)
	}
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return Task{}, fmt.Errorf("%s cannot be read", path)
	}
	return a.qemuTask("Install QEMU from a file", "qemu", "install", "--file", path, "--use", "-y"), nil
}

// UseOfficialQEMU checks the official QEMU and makes it the default.
func (a *App) UseOfficialQEMU() (Task, error) {
	a.CheckQEMUAgain()
	return a.qemuTask("Use the official QEMU", "qemu", "use", qemu.OfficialID), nil
}

func checkQEMUID(id string) error {
	if id != qemu.OfficialID && !qemu.ValidTag(id) {
		return fmt.Errorf("%q is not a QEMU build", id)
	}
	return nil
}

func (a *App) UseQEMU(id string) (Task, error) {
	if err := checkQEMUID(id); err != nil {
		return Task{}, err
	}
	return a.qemuTask("Use QEMU "+id, "qemu", "use", id), nil
}

// RemoveQEMU needs confirmed: a project build's files are deleted.
func (a *App) RemoveQEMU(id string, confirmed bool) (Task, error) {
	if err := checkQEMUID(id); err != nil {
		return Task{}, err
	}
	if !confirmed {
		return Task{}, fmt.Errorf("removing QEMU build %s was not confirmed", id)
	}
	return a.qemuTask("Remove QEMU "+id, "qemu", "rm", id, "-y"), nil
}

// PickQEMUArchive shows a file dialog for a pvm-qemu archive; it returns
// "" when cancelled.
func (a *App) PickQEMUArchive() (string, error) {
	if a.pickFile == nil {
		return "", errors.New("no file dialog here")
	}
	return a.pickFile("Choose a pvm-qemu archive", "*.tar.gz")
}
