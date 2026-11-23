package desktop

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/huangzheng2016/pvm/internal/apps"
	"github.com/huangzheng2016/pvm/internal/exit"
	"github.com/huangzheng2016/pvm/internal/image"
	"github.com/huangzheng2016/pvm/internal/vm"
	"github.com/huangzheng2016/pvm/internal/workspace"
)

// Words the CLI reads as a subcommand where a VM name may stand, so a VM
// with such a name cannot be named there.
var (
	routeWords = []string{"ls", "add", "rm", "default", "explain"}
	appWords   = []string{"ls", "list", "stop", "attach", "config", "catalog", "install"}
)

func checkVM(name string) error { return vm.ValidateName(name) }

func checkVMNot(name, command string, words []string) error {
	if err := checkVM(name); err != nil {
		return err
	}
	if slices.Contains(words, name) {
		return fmt.Errorf("`pvm %s %s` is a subcommand, so a vm named %q cannot be used here", command, name, name)
	}
	return nil
}

func checkExit(name string) error {
	if !exit.ValidName(name) {
		return fmt.Errorf("invalid exit name %q", name)
	}
	return nil
}

// checkVia accepts direct or a registered exit.
func (a *App) checkVia(via string) error {
	if via == direct {
		return nil
	}
	if err := checkExit(via); err != nil {
		return err
	}
	if _, err := exit.LoadEntry(a.exitsDir(), via); err != nil {
		return fmt.Errorf("exit %q is not registered", via)
	}
	return nil
}

func one(argv ...string) []step {
	return []step{{argv: argv, display: commandLine(argv...)}}
}

// startStep starts a VM and follows it. The CLI's own wait is replaced
// by the app's, so the stages show; a person runs the plain command.
func startStep(name string) step {
	return step{argv: []string{"start", name, "--no-wait"}, display: commandLine("start", name), wait: name}
}

// StartDaemon runs `pvm daemon start`, which also clears the mark left
// by `pvm daemon stop`.
func (a *App) StartDaemon() (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	return a.runTask("Start the daemon", one("daemon", "start")), nil
}

// StopDaemon needs confirmed: the page asks the user, as the daemon
// shuts every running VM down on its way out. `pvm daemon stop` leaves
// the mark that keeps the app from starting it again by itself.
func (a *App) StopDaemon(confirmed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if !confirmed {
		return Task{}, errors.New("stopping the daemon was not confirmed")
	}
	return a.runTask("Stop the daemon", one("daemon", "stop")), nil
}

// upSteps bring a VM up before an action that needs it running, once
// the user agreed to start it: a stopped VM is started and a starting one
// waited for, so the action runs when it is ready. A VM that is up or
// failed needs none, as `pvm app` takes a failed VM too.
func (a *App) upSteps(name string, start bool) ([]step, error) {
	if !start {
		return nil, nil
	}
	in, err := a.findVM(name)
	if err != nil {
		return nil, err
	}
	switch state, _, _ := vmState(in); state {
	case stateStopped:
		return []step{startStep(name)}, nil
	case stateStarting:
		// only waits; a person would wait with `pvm start`
		return []step{{display: commandLine("start", name), wait: name}}, nil
	}
	return nil, nil
}

func (a *App) StartVM(name string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	return a.runTask("Start "+name, []step{startStep(name)}), nil
}

func (a *App) StopVM(name string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	return a.runTask("Stop "+name, one("stop", name)), nil
}

// DeleteVM needs confirmed: the page asks the user, naming the VM.
func (a *App) DeleteVM(name string, confirmed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	if !confirmed {
		return Task{}, fmt.Errorf("deleting %s was not confirmed", name)
	}
	return a.runTask("Delete "+name, one("rm", name, "-y")), nil
}

func (a *App) OpenTerminal(name string) error {
	if err := a.native(); err != nil {
		return err
	}
	if err := checkVM(name); err != nil {
		return err
	}
	return a.vmTerminal("ssh", name)
}

// vmTerminal opens `pvm ssh` in the VM's home directory. pvm ssh enters
// the guest folder of a workspace holding its working directory, and the
// child would inherit the app's own; the host home directory cannot be
// a workspace.
func (a *App) vmTerminal(args ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return a.sys.openTerminal(home, args)
}

// appTarget turns what the user typed into the arguments of `pvm app`:
// a URL as it is, anything else as a command split at spaces.
func appTarget(target string) ([]string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("enter a command or a URL")
	}
	if strings.ContainsFunc(target, unicode.IsControl) {
		return nil, errors.New("the command contains a control character")
	}
	// The command is split at spaces, so a quote would not group words,
	// and no shell could be given the copied command safely.
	if strings.Contains(target, `"`) {
		return nil, errors.New(`a double quote cannot be passed: the command is split at spaces (write %22 in a URL)`)
	}
	fields := strings.Fields(target)
	if u, err := url.Parse(target); len(fields) == 1 && err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return fields, nil
	}
	return append([]string{"--"}, fields...), nil
}

// RunApp shows a command or a URL of the VM as a host window. start is
// that the user agreed to start the VM first if it is stopped; without it
// `pvm app` refuses a stopped VM.
func (a *App) RunApp(name, target string, start bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVMNot(name, "app", appWords); err != nil {
		return Task{}, err
	}
	args, err := appTarget(target)
	if err != nil {
		return Task{}, err
	}
	return a.runApp("Open an app in "+name, name, start, append([]string{"app", name}, args...))
}

// runApp runs `pvm app`, after the steps that bring the VM up.
func (a *App) runApp(title, name string, start bool, argv []string) (Task, error) {
	steps, err := a.upSteps(name, start)
	if err != nil {
		return Task{}, err
	}
	return a.runTask(title, append(steps, one(argv...)...)), nil
}

// RunCatalogApp starts a catalog program with a window through `pvm app`.
// A terminal program is refused: it works on the folder it starts in, and
// this would start it in ~; a workspace terminal starts in the right one.
func (a *App) RunCatalogApp(name, id string, start bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVMNot(name, "app", appWords); err != nil {
		return Task{}, err
	}
	entry, err := a.catalogApp(id)
	if err != nil {
		return Task{}, err
	}
	if !entry.Window {
		return Task{}, fmt.Errorf("%s runs in a terminal, in the folder it should work on: open a workspace's terminal and run %s there", entry.Name, entry.Start)
	}
	return a.runApp("Open "+entry.Name+" in "+name, name, start, append([]string{"app", name, "--"}, strings.Fields(entry.Start)...))
}

func (a *App) catalogApp(id string) (CatalogApp, error) {
	if !apps.ValidID(id) {
		return CatalogApp{}, fmt.Errorf("invalid app id %q", id)
	}
	catalog, err := a.readCatalog()
	if err != nil {
		return CatalogApp{}, err
	}
	for _, c := range catalog {
		if c.ID == id {
			return c, nil
		}
	}
	return CatalogApp{}, fmt.Errorf("unknown app %q", id)
}

// OpenFiles shows the VM's file manager.
func (a *App) OpenFiles(name string, start bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVMNot(name, "app", appWords); err != nil {
		return Task{}, err
	}
	return a.runApp("Open files in "+name, name, start, []string{"app", name, "--", "thunar"})
}

// AppConfig is what `pvm app config` sets. An empty field is left as it
// is in the VM.
type AppConfig struct {
	Clipboard string `json:"clipboard"` // off, to-guest, to-host or both
	Drop      string `json:"drop"`      // on or off
	Audio     string `json:"audio"`     // off, out or both
	// AudioSilence is skip or send: whether the VM sends sound while it
	// plays nothing.
	AudioSilence string `json:"audio_silence"`
	// RenderCPUs is the vCPUs a Chromium app's renderers may use, as a
	// number; 0 is no limit.
	RenderCPUs string `json:"render_cpus"`
	// Scale is how much the host enlarges the VM's windows: 1, 1.25, 1.5
	// or 2.
	Scale string `json:"scale"`
	// Keyring is off or host: whether pvm encrypts the VM's login keyring
	// with a password kept on the host and unlocks it for the apps.
	Keyring string `json:"keyring"`
}

// maxRenderCPUs is the daemon's bound on --render-cpus.
const maxRenderCPUs = 256

func (c AppConfig) args() ([]string, error) {
	var args []string
	for _, f := range []struct {
		flag, value string
		allowed     []string
	}{
		{"--clipboard", c.Clipboard, []string{"off", "to-guest", "to-host", "both"}},
		{"--drop", c.Drop, []string{"on", "off"}},
		{"--audio", c.Audio, []string{"off", "out", "both"}},
		{"--audio-silence", c.AudioSilence, []string{"skip", "send"}},
		{"--scale", c.Scale, []string{"1", "1.25", "1.5", "2"}},
		{"--keyring", c.Keyring, []string{"off", "host"}},
	} {
		if f.value == "" {
			continue
		}
		if !slices.Contains(f.allowed, f.value) {
			return nil, fmt.Errorf("%s must be one of %s", f.flag, strings.Join(f.allowed, ", "))
		}
		args = append(args, f.flag, f.value)
	}
	if c.RenderCPUs != "" {
		n, err := strconv.Atoi(c.RenderCPUs)
		if err != nil || n < 0 || n > maxRenderCPUs || strconv.Itoa(n) != c.RenderCPUs {
			return nil, fmt.Errorf("--render-cpus must be a number from 0 (no limit) to %d", maxRenderCPUs)
		}
		args = append(args, "--render-cpus", c.RenderCPUs)
	}
	return args, nil
}

func (a *App) ConfigureApps(name string, c AppConfig) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	args, err := c.args()
	if err != nil {
		return Task{}, err
	}
	if len(args) == 0 {
		return Task{}, errors.New("nothing to change")
	}
	return a.runTask("App settings of "+name, one(append([]string{"app", "config", name}, args...)...)), nil
}

// installStep checks the ids against the catalog. A VM named like a
// catalog app is refused: `pvm app install` would read it as an app.
func (a *App) installStep(name string, ids []string) (step, error) {
	if len(ids) == 0 {
		return step{}, errors.New("choose a program to install")
	}
	if _, err := a.catalogApp(name); err == nil {
		return step{}, fmt.Errorf("`pvm app install` reads %q as a program, so nothing can be installed in a vm of that name from here", name)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, err := a.catalogApp(id); err != nil {
			return step{}, err
		}
		if seen[id] {
			return step{}, fmt.Errorf("app %q is listed twice", id)
		}
		seen[id] = true
	}
	argv := append([]string{"app", "install", name}, ids...)
	return step{argv: argv, display: commandLine(argv...)}, nil
}

// InstallApps installs catalog programs in a running VM.
func (a *App) InstallApps(name string, ids []string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	s, err := a.installStep(name, ids)
	if err != nil {
		return Task{}, err
	}
	return a.runTask("Install programs in "+name, []step{s}), nil
}

func (a *App) SetDefaultVM(name string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	return a.runTask("Default vm: "+name, one("default-vm", name)), nil
}

func (a *App) ClearDefaultVM() (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	return a.runTask("Clear the default vm", one("default-vm", "--clear")), nil
}

// SetVMExit needs confirmed: a running VM is stopped and started again
// to apply the route.
func (a *App) SetVMExit(name, via string, confirmed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVMNot(name, "route", routeWords); err != nil {
		return Task{}, err
	}
	if err := a.checkVia(via); err != nil {
		return Task{}, err
	}
	if !confirmed {
		return Task{}, fmt.Errorf("changing the exit of %s was not confirmed; a running vm restarts", name)
	}
	return a.runTask("Exit of "+name+": "+via, one("route", name, "--via", via)), nil
}

func (a *App) SetDefaultExit(via string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := a.checkVia(via); err != nil {
		return Task{}, err
	}
	return a.runTask("Default exit: "+via, one("route", "default", "--via", via)), nil
}

// CreateRequest is the Create form.
type CreateRequest struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	User   string `json:"user"`
	CPUs   int    `json:"cpus"`
	MemMB  int    `json:"mem_mb"`
	DiskGB int    `json:"disk_gb"`
	// Exit is direct or an exit name; empty follows the default exit.
	Exit      string    `json:"exit"`
	AppConfig AppConfig `json:"app_config"`
	// Apps are the catalog programs to install once the VM is ready.
	Apps []string `json:"apps"`
}

func (r CreateRequest) check() error {
	if err := checkVM(r.Name); err != nil {
		return err
	}
	if !slices.Contains(image.Supported(), r.Image) {
		return fmt.Errorf("unknown image %q", r.Image)
	}
	if err := vm.ValidateSpec(&vm.Spec{User: r.User}, nil); err != nil {
		return err
	}
	return Resources{CPUs: r.CPUs, MemMB: r.MemMB, DiskGB: r.DiskGB}.check()
}

// Resources are the vcpus, memory and disk of a VM.
type Resources struct {
	CPUs   int `json:"cpus"`
	MemMB  int `json:"mem_mb"`
	DiskGB int `json:"disk_gb"`
}

// check applies the daemon's own limits.
func (r Resources) check() error {
	for _, n := range []struct {
		name          string
		v, least, max int
	}{{"cpus", r.CPUs, 1, 64}, {"memory (MB)", r.MemMB, 128, 1048576}, {"disk (GB)", r.DiskGB, 1, 4096}} {
		if n.v < n.least || n.v > n.max {
			return fmt.Errorf("%s %d out of range [%d,%d]", n.name, n.v, n.least, n.max)
		}
	}
	return nil
}

// ResizeVM brings a VM to r, passing `pvm resize` only what differs; the
// disk can only grow. `pvm resize` takes a stopped VM, so a running one
// is stopped first and started again afterwards, which restart must
// allow: the page tells the user before it asks.
func (a *App) ResizeVM(name string, r Resources, restart bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkVM(name); err != nil {
		return Task{}, err
	}
	if err := r.check(); err != nil {
		return Task{}, err
	}
	infos, err := a.readVMs()
	if err != nil {
		return Task{}, err
	}
	at := slices.IndexFunc(infos, func(in vmInfo) bool { return in.Spec.Name == name && in.Spec.Role != "gateway" })
	if at < 0 {
		return Task{}, fmt.Errorf("vm %s does not exist", name)
	}
	now := infos[at]
	argv := []string{"resize", name}
	if r.DiskGB < now.Spec.DiskGB {
		return Task{}, fmt.Errorf("the disk of %s is %d GB and can only grow", name, now.Spec.DiskGB)
	}
	if r.DiskGB > now.Spec.DiskGB {
		argv = append(argv, strconv.Itoa(r.DiskGB))
	}
	if r.CPUs != now.Spec.CPUs {
		argv = append(argv, "--cpus", strconv.Itoa(r.CPUs))
	}
	if r.MemMB != now.Spec.MemMB {
		argv = append(argv, "--mem", strconv.Itoa(r.MemMB))
	}
	if len(argv) == 2 {
		return Task{}, errors.New("nothing to change")
	}
	steps := one(argv...)
	if now.Running {
		if !restart {
			return Task{}, fmt.Errorf("%s is running, and restarting it to resize was not confirmed", name)
		}
		steps = append(append(one("stop", name), steps...), startStep(name))
	}
	return a.runTask("Resize "+name, steps), nil
}

// CreateVM creates a VM, sets its exit and app settings, waits until it
// is ready and installs the chosen programs.
//
// The VM is created stopped and started only after its exit is set, so
// it never sends anything through another exit: a failed route step
// leaves it stopped. A chosen exit is always set on the VM itself, also
// when it is the default one, so a later change of the default does not
// move the VM.
func (a *App) CreateVM(r CreateRequest) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := r.check(); err != nil {
		return Task{}, err
	}
	config, err := r.AppConfig.args()
	if err != nil {
		return Task{}, err
	}
	route := r.Exit != ""
	if route {
		if err := a.checkVia(r.Exit); err != nil {
			return Task{}, err
		}
		if err := checkVMNot(r.Name, "route", routeWords); err != nil {
			return Task{}, err
		}
	}
	var install []step
	if len(r.Apps) > 0 {
		s, err := a.installStep(r.Name, r.Apps)
		if err != nil {
			return Task{}, err
		}
		install = []step{s}
	}

	steps := one("create", "--name", r.Name, "--image", r.Image, "--user", r.User,
		"--cpus", strconv.Itoa(r.CPUs), "--mem", strconv.Itoa(r.MemMB), "--disk", strconv.Itoa(r.DiskGB), "--no-start")
	if route {
		steps = append(steps, one("route", r.Name, "--via", r.Exit)...)
	}
	if len(config) > 0 {
		steps = append(steps, one(append([]string{"app", "config", r.Name}, config...)...)...)
	}
	steps = append(steps, startStep(r.Name))
	return a.runTask("Create "+r.Name, append(steps, install...)), nil
}

// AddExitRequest is the form for a pvm exit-agent node.
type AddExitRequest struct {
	Name   string `json:"name"`
	Server string `json:"server"` // host:port
	Token  string `json:"token"`
}

func checkNewExit(name string) error {
	if err := checkExit(name); err != nil {
		return err
	}
	if name == direct {
		return fmt.Errorf("an exit cannot be named %q", direct)
	}
	return nil
}

// AddExit hands the token to the CLI in a private file under the run
// directory, removed when the command ends: a token on the command line
// would show in the process list.
func (a *App) AddExit(r AddExitRequest) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkNewExit(r.Name); err != nil {
		return Task{}, err
	}
	if host, port, err := net.SplitHostPort(r.Server); err != nil || host == "" || port == "" ||
		strings.HasPrefix(r.Server, "-") || strings.ContainsFunc(r.Server, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
		return Task{}, errors.New("the server must be host:port")
	}
	if r.Token == "" || strings.ContainsFunc(r.Token, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }) {
		return Task{}, errors.New("the token must be one word")
	}
	f, err := os.CreateTemp(a.p.Run, tokenFilePattern)
	if err != nil {
		return Task{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(r.Token + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Task{}, errors.New("cannot write the token file")
	}
	return a.runTask("Add exit "+r.Name, []step{{
		argv:    []string{"exit", "add", r.Name, "--server", r.Server, "--token-file", f.Name()},
		display: commandLine("exit", "add", r.Name, "--server", r.Server, "--token-file", "<token file>"),
		secrets: []string{r.Token},
	}}), nil
}

func (a *App) AddWarpExit(name string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkNewExit(name); err != nil {
		return Task{}, err
	}
	return a.runTask("Add exit "+name, one("exit", "add-warp", name)), nil
}

func (a *App) TestExit(name string, speed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkExit(name); err != nil {
		return Task{}, err
	}
	argv := []string{"exit", "test", name}
	if speed {
		argv = append(argv, "--speed")
	}
	return a.runTask("Test exit "+name, one(argv...)), nil
}

// RemoveExit needs confirmed. `pvm exit rm` does not look at who uses
// the exit, so the refusal names the users for the page to show.
func (a *App) RemoveExit(name string, confirmed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := checkExit(name); err != nil {
		return Task{}, err
	}
	if !confirmed {
		e, err := exit.LoadEntry(a.exitsDir(), name)
		if err != nil {
			return Task{}, fmt.Errorf("exit %q is not registered", name)
		}
		infos, err := a.readVMs()
		if err != nil {
			return Task{}, err
		}
		rt, err := a.readRoutes()
		if err != nil {
			return Task{}, err
		}
		v := exitView(e, infos, rt)
		var users []string
		if v.Default {
			users = append(users, "the default exit")
		}
		for _, name := range v.VMs {
			users = append(users, "vm "+name)
		}
		for _, rule := range v.Rules {
			users = append(users, "rule "+rule)
		}
		if len(users) > 0 {
			return Task{}, fmt.Errorf("removing exit %s was not confirmed; it is still used by: %s", name, strings.Join(users, ", "))
		}
		return Task{}, fmt.Errorf("removing exit %s was not confirmed", name)
	}
	return a.runTask("Remove exit "+name, one("exit", "rm", name)), nil
}

func (a *App) loadWorkspace(id string) (*workspace.Workspace, error) {
	if err := workspace.ValidID(id); err != nil {
		return nil, err
	}
	return workspace.NewStore(a.p).Load(id)
}

// workspaceVM returns the VM argument of a command run from the
// workspace's host folder. A workspace without a VM leaves it to the CLI.
func workspaceVM(ws *workspace.Workspace, command string, words []string) ([]string, error) {
	if ws.VM == "" {
		return nil, nil
	}
	if err := checkVMNot(ws.VM, command, words); err != nil {
		return nil, err
	}
	return []string{ws.VM}, nil
}

func (a *App) OpenWorkspaceFolder(id string) error {
	if err := a.native(); err != nil {
		return err
	}
	ws, err := a.loadWorkspace(id)
	if err != nil {
		return err
	}
	return a.sys.openFolder(ws.HostPath)
}

// OpenWorkspaceTerminal runs `pvm ssh` in the host folder, which starts
// the session in the matching guest folder.
func (a *App) OpenWorkspaceTerminal(id string) error {
	if err := a.native(); err != nil {
		return err
	}
	ws, err := a.loadWorkspace(id)
	if err != nil {
		return err
	}
	vmArg, err := workspaceVM(ws, "ssh", nil)
	if err != nil {
		return err
	}
	return a.sys.openTerminal(ws.HostPath, append([]string{"ssh"}, vmArg...))
}

// RunWorkspaceApp runs `pvm app` from the host folder, so a command
// starts in the matching guest folder. start is as for RunApp.
func (a *App) RunWorkspaceApp(id, target string, start bool) (Task, error) {
	return a.workspaceApp(id, "Open an app in workspace "+id, start, func(*workspace.Workspace) ([]string, error) {
		return appTarget(target)
	})
}

// OpenWorkspaceFiles shows the file manager in the guest folder.
func (a *App) OpenWorkspaceFiles(id string, start bool) (Task, error) {
	return a.workspaceApp(id, "Open files in workspace "+id, start, func(ws *workspace.Workspace) ([]string, error) {
		return []string{"--", "thunar", ws.GuestPath}, nil
	})
}

func (a *App) workspaceApp(id, title string, start bool, target func(*workspace.Workspace) ([]string, error)) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	ws, err := a.loadWorkspace(id)
	if err != nil {
		return Task{}, err
	}
	vmArg, err := workspaceVM(ws, "app", appWords)
	if err != nil {
		return Task{}, err
	}
	args, err := target(ws)
	if err != nil {
		return Task{}, err
	}
	// a workspace without a VM leaves the choice to `pvm app`, so there is
	// none to start
	steps, err := a.upSteps(ws.VM, start && ws.VM != "")
	if err != nil {
		return Task{}, err
	}
	argv := append(append([]string{"app"}, vmArg...), args...)
	return a.runTask(title, append(steps, step{argv: argv, dir: ws.HostPath, display: commandLine(argv...)})), nil
}

// SyncWorkspace runs `pvm workspace push` or `pull`.
func (a *App) SyncWorkspace(id, direction string) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := workspace.ValidID(id); err != nil {
		return Task{}, err
	}
	if direction != "push" && direction != "pull" {
		return Task{}, fmt.Errorf("direction must be push or pull, not %q", direction)
	}
	return a.runTask("Workspace "+id+": "+direction, one("workspace", direction, id)), nil
}

// CreateWorkspaceRequest is the workspace form.
type CreateWorkspaceRequest struct {
	ID       string `json:"id"`
	VM       string `json:"vm"`
	Mode     string `json:"mode"` // mount, sync or copy
	HostPath string `json:"host_path"`
	ReadOnly bool   `json:"read_only"`
	// Confirmed lets the CLI start a stopped VM and upload more than
	// 1 GiB, which it otherwise asks about.
	Confirmed bool `json:"confirmed"`
}

func (a *App) CreateWorkspace(r CreateWorkspaceRequest) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := workspace.ValidID(r.ID); err != nil {
		return Task{}, err
	}
	if err := checkVM(r.VM); err != nil {
		return Task{}, err
	}
	mode, err := workspace.ParseMode(r.Mode)
	if err != nil {
		return Task{}, err
	}
	if r.ReadOnly && mode != workspace.ModeMount {
		return Task{}, errors.New("read-only applies to mount mode only")
	}
	if !filepath.IsAbs(r.HostPath) {
		return Task{}, errors.New("choose the host folder")
	}
	argv := []string{"workspace", "create", r.ID, r.VM, "--mode", r.Mode, "--path", r.HostPath}
	if r.ReadOnly {
		argv = append(argv, "--read-only")
	}
	if r.Confirmed {
		argv = append(argv, "-y")
	}
	return a.runTask("Create workspace "+r.ID, one(argv...)), nil
}

// RemoveWorkspace needs confirmed. It forgets the workspace; the files
// on both sides stay.
func (a *App) RemoveWorkspace(id string, confirmed bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if err := workspace.ValidID(id); err != nil {
		return Task{}, err
	}
	if !confirmed {
		return Task{}, fmt.Errorf("removing workspace %s was not confirmed", id)
	}
	return a.runTask("Remove workspace "+id, one("workspace", "rm", id, "-y")), nil
}

// PickFolder shows the folder dialog; it returns "" when cancelled.
func (a *App) PickFolder() (string, error) {
	if err := a.native(); err != nil {
		return "", err
	}
	if a.pick == nil {
		return "", errors.New("no folder dialog here")
	}
	return a.pick()
}

// SetDaemonAtLogin adds or removes the pvm-daemon scheduled task.
func (a *App) SetDaemonAtLogin(on bool) (Task, error) {
	if err := a.native(); err != nil {
		return Task{}, err
	}
	if on {
		return a.runTask("Start the daemon at login", one("daemon", "install")), nil
	}
	return a.runTask("Do not start the daemon at login", one("daemon", "uninstall")), nil
}

func (a *App) SetAppAtLogin(on bool) error {
	if err := a.native(); err != nil {
		return err
	}
	return a.sys.setAppAtLogin(on)
}
