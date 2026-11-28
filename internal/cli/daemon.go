package cli

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/huangzheng2016/pvm/internal/assets"
	"github.com/huangzheng2016/pvm/internal/cluster"
	"github.com/huangzheng2016/pvm/internal/config"
	"github.com/huangzheng2016/pvm/internal/daemon"
	"github.com/huangzheng2016/pvm/internal/diaglog"
	"github.com/huangzheng2016/pvm/internal/paths"
	"github.com/huangzheng2016/pvm/internal/qemu"
)

var tcgWarningOnce sync.Once

func shouldWarnTCG(accel qemu.Accel, forced string) bool {
	return accel == qemu.AccelTCG && forced != "tcg"
}

func tcgHint(goos, wslDistro string) string {
	if goos == "windows" {
		return "warning: using TCG software emulation; for WHPX run `Enable-WindowsOptionalFeature -Online -FeatureName HypervisorPlatform -All` in elevated PowerShell and reboot; for WSL2 set nestedVirtualization=true in %UserProfile%\\.wslconfig, run `wsl --shutdown`, verify /dev/kvm, and use PVM_BACKEND=wsl"
	}
	if goos == "linux" && wslDistro != "" {
		return "warning: using TCG software emulation in WSL2; add [wsl2] nestedVirtualization=true to %UserProfile%\\.wslconfig, run `wsl --shutdown`, restart WSL, and verify /dev/kvm"
	}
	return ""
}

func warnTCGAccel(accel qemu.Accel) {
	if !shouldWarnTCG(accel, os.Getenv("PVM_QEMU_ACCEL")) {
		return
	}
	hint := tcgHint(runtime.GOOS, os.Getenv("WSL_DISTRO_NAME"))
	if hint == "" {
		return
	}
	tcgWarningOnce.Do(func() { fmt.Fprintln(os.Stderr, hint) })
}

func warnTCG(c *daemon.Client) {
	var st struct {
		Accel qemu.Accel `json:"accel"`
	}
	if err := c.Get("/v1/status", &st); err == nil {
		warnTCGAccel(st.Accel)
	}
}

func CmdDaemon(args []string) int {
	if len(args) > 0 && args[0] == "status" {
		args = append([]string{"status"}, scanJSONFlag(args[1:])...)
	}
	if len(args) == 0 {
		return fatalf("usage: pvm daemon run|start|stop|status|restart|install|uninstall")
	}
	p, err := mustPaths()
	if err != nil {
		return fatalf("%v", err)
	}
	switch args[0] {
	case "run":
		return daemonRun(p)
	case "start":
		os.Remove(p.DaemonStopMark())
		if _, err := daemon.Ensure(p); err != nil {
			return fatalf("%v", err)
		}
		fmt.Println("daemon started")
		return 0
	case "stop":
		os.WriteFile(p.DaemonStopMark(), []byte("1\n"), 0o600)
		c := daemon.NewClient(p)
		if !c.Healthy() {
			fmt.Println("daemon not running")
			return 0
		}
		if err := c.Post("/v1/shutdown", nil, nil); err != nil {
			return fatalf("%v", err)
		}
		for i := 0; i < 900 && c.Healthy(); i++ {
			if i > 0 && i%50 == 0 {
				fmt.Printf("  waiting for daemon to stop (%ds; vms shutting down in parallel)\n", i/5)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if c.Healthy() {
			return fatalf("daemon is still running after 180s; not stopped")
		}
		fmt.Println("daemon stopped")
		return 0
	case "restart":
		restore, code := restartDaemon(p)
		if code != 0 || len(restore) == 0 {
			if code == 0 {
				fmt.Println("daemon restarted")
			}
			return code
		}
		// The new daemon brings these back on its own; waiting on each
		// keeps restart from returning before they are ready.
		fmt.Printf("daemon restarted; waiting for %s to come back\n", strings.Join(restore, ", "))
		var failed []string
		for _, name := range restore {
			if CmdStart([]string{name}) != 0 {
				failed = append(failed, name)
			}
		}
		if len(failed) != 0 {
			return fatalf("daemon restarted, but %s did not come back; run `pvm start %s`", strings.Join(failed, ", "), failed[0])
		}
		fmt.Println("daemon restarted; running vms are back")
		return 0
	case "status":
		c := daemon.NewClient(p)
		if !c.Healthy() {
			if jsonOutput {
				return failErr(writeJSONOut(map[string]any{"status": "completed", "data": map[string]any{"running": false}}))
			}
			fmt.Println("daemon: not running")
			return 0
		}
		var st map[string]any
		if err := c.Get("/v1/status", &st); err != nil {
			return fatalf("%v", err)
		}
		if jsonOutput {
			st["running"] = true
			return failErr(writeJSONOut(map[string]any{"status": "completed", "data": st}))
		}
		fmt.Printf("daemon: running\n  pid:     %v\n  version: %v\n  ready:   %v\n  uptime:  %v\n",
			st["pid"], st["version"], st["ready"], st["uptime"])
		printDaemonQEMU(st)
		fmt.Printf("  vms:     %v (%v running)\n", st["vms"], st["running"])
		if n, _ := st["tcg_fallback"].(float64); n > 0 {
			hint := "on macOS a reboot usually restores it"
			if runtime.GOOS == "windows" {
				hint = "enable HypervisorPlatform and reboot; for WSL2 set nestedVirtualization=true in .wslconfig and verify /dev/kvm"
			} else if runtime.GOOS == "linux" {
				hint = "check /dev/kvm permissions"
				if os.Getenv("WSL_DISTRO_NAME") != "" {
					hint = "set nestedVirtualization=true in .wslconfig, run `wsl --shutdown`, restart WSL, and verify /dev/kvm"
				}
			}
			fmt.Printf("  warning: %v vm(s) running under tcg fallback; host accel %v is unavailable (%s)\n", n, st["accel"], hint)
		}
		return 0
	case "install":
		if err := daemon.ServiceInstall(p); err != nil {
			return fatalf("%v", err)
		}
		fmt.Println("service installed and started")
		return 0
	case "uninstall":
		if err := daemon.ServiceUninstall(p); err != nil {
			return fatalf("%v", err)
		}
		fmt.Println("service uninstalled")
		return 0
	}
	return fatalf("unknown daemon subcommand %q", args[0])
}

func daemonRun(p *paths.Paths) int {
	if lw, err := diaglog.NewRotateWriter(p.DaemonLog(), diaglog.DefaultDaemonMaxBytes, 2, 0o600); err == nil {
		defer lw.Close()
		log.SetOutput(diaglog.NewRedactWriter(lw, diaglog.RedactorForPaths(p)))
	}
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return fatalf("config: %v", err)
	}
	if err := assets.Unpack(p.Runtime); err != nil {
		return fatalf("assets: %v", err)
	}

	clusters, err := cluster.LoadStore(p.Clusters)
	if err != nil {
		return fatalf("clusters: %v", err)
	}
	// The daemon runs without a usable QEMU: it resolves the selected
	// build at each VM start and reports what blocks it.
	srv := daemon.NewServer(p, cfg, nil, "", clusters)
	fmt.Fprintf(os.Stderr, "pvm daemon: sock=%s\n", p.DaemonSock())
	if err := daemon.Run(p, srv); err != nil {
		return fatalf("daemon: %v", err)
	}
	return 0
}

// printDaemonQEMU prints the daemon's QEMU build, or what blocks VMs.
func printDaemonQEMU(st map[string]any) {
	b, hasBuild := st["qemu_build"].(map[string]any)
	problems, _ := st["qemu_problems"].([]any)
	switch {
	case hasBuild:
		fmt.Printf("  accel:   %v", st["accel"])
		if forced, _ := st["accel_forced"].(bool); forced {
			fmt.Print(" (forced by PVM_QEMU_ACCEL)")
		}
		fmt.Printf("\n  qemu:    %v %v\n", b["label"], b["path"])
	case len(problems) > 0:
		fmt.Printf("  qemu:    none usable: %v\n", problems[0])
	default:
		fmt.Printf("  qemu:    %v\n  note:    the running daemon is pvm %v, older than this pvm; restart it: pvm daemon restart\n", st["qemu"], st["version"])
	}
	if changed, ok := st["qemu_changed"].([]any); ok {
		for _, c := range changed {
			if m, ok := c.(map[string]any); ok {
				fmt.Printf("  note:    %v still runs on %v until it restarts\n", m["vm"], m["build"])
			}
		}
	}
	if blocked, ok := st["blocked_clusters"].([]any); ok {
		for _, b := range blocked {
			if m, ok := b.(map[string]any); ok {
				fmt.Printf("  note:    cluster %v is blocked: %v\n", m["cluster"], m["reason"])
			}
		}
	}
}

// restartDaemon stops the daemon so that the next one restores its running
// VMs, starts the next one, and returns the user VMs it will restore.
func restartDaemon(p *paths.Paths) ([]string, int) {
	os.Remove(p.DaemonStopMark())
	c := daemon.NewClient(p)
	var restore []string
	if c.Healthy() {
		var err error
		if restore, err = runningUserVMs(c); err != nil {
			return nil, fatalf("list vms: %v", err)
		}
		if err := c.Post("/v1/shutdown?restore=1", nil, nil); err != nil {
			return nil, fatalf("shutdown: %v", err)
		}
		for i := 0; i < 900 && c.Healthy(); i++ {
			if i > 0 && i%50 == 0 {
				fmt.Printf("  waiting for old daemon to stop (%ds)\n", i/5)
			}
			time.Sleep(200 * time.Millisecond)
		}
		if c.Healthy() {
			return nil, fatalf("old daemon is still running after 180s; refusing to start a second one")
		}
	}
	if _, err := daemon.Ensure(p); err != nil {
		return nil, fatalf("%v", err)
	}
	return restore, 0
}

// restartDaemonOnly restarts the daemon without waiting for its VMs, for
// callers that start each VM themselves.
func restartDaemonOnly() int {
	p, err := mustPaths()
	if err != nil {
		return fatalf("%v", err)
	}
	_, code := restartDaemon(p)
	return code
}
