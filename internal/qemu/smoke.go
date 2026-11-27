package qemu

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/native"
)

// Smoke starts the build with pvm's own argv, paused under TCG, and drives
// it over QMP as the daemon does. Help output cannot show everything that
// argv depends on (smbios, a serial chardev with a logfile, user netdev
// forwards, the virtio devices, 9p), and qemu-img's -B and -b convert forms
// are what snapshots and compaction use. It runs at install, use, check and
// after a binary changes, never per start.
func Smoke(b *Build) error {
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp" // unix socket paths must stay short
	}
	dir, err := os.MkdirTemp(base, "pvmq")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	img := func(args ...string) error {
		cmd := exec.Command(b.Img, args...)
		cmd.Dir = dir
		fsutil.ConfigureHiddenCmd(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("qemu-img %s: %s", args[0], firstLine(strings.TrimSpace(string(out))))
		}
		return nil
	}
	if err := img("create", "-q", "-f", "qcow2", "base.qcow2", "64M"); err != nil {
		return err
	}
	if err := img("create", "-q", "-f", "qcow2", "-b", "base.qcow2", "-F", "qcow2", "disk.qcow2"); err != nil {
		return err
	}
	// Snapshots pass -B; compaction passes the flag this version takes.
	v, _ := parseVersion("QEMU emulator version " + b.Version)
	flags := []string{"-B"}
	if f := convertBackingFlag(v); f != "-B" {
		flags = append(flags, f)
	}
	for _, flag := range flags {
		if err := img("convert", "-f", "qcow2", "-O", "qcow2", flag, "base.qcow2", "-F", "qcow2", "disk.qcow2", "out"+flag+".qcow2"); err != nil {
			return fmt.Errorf("%v (convert %s)", err, flag)
		}
	}
	seed := filepath.Join(dir, "seed.img")
	if err := os.WriteFile(seed, make([]byte, 1<<20), 0o600); err != nil {
		return err
	}
	vars := ""
	if native.Arch == "arm64" {
		vars = filepath.Join(dir, "vars.fd")
		if err := os.WriteFile(vars, make([]byte, 64<<20), 0o600); err != nil {
			return err
		}
	}
	sock := func(name string) (string, error) {
		if runtime.GOOS == "windows" {
			port, err := freePort()
			return "tcp:127.0.0.1:" + strconv.Itoa(port), err
		}
		return filepath.Join(dir, name+".sock"), nil
	}
	var socks [4]string
	for i, name := range []string{"qmp", "serial", "agent", "stream"} {
		if socks[i], err = sock(name); err != nil {
			return err
		}
	}
	sshPort, err := freePort()
	if err != nil {
		return err
	}
	share := filepath.Join(dir, "share")
	if err := os.MkdirAll(share, 0o700); err != nil {
		return err
	}
	cfg := VMConfig{
		Name: "smoke", CPUs: 1, MemMB: 256,
		DiskPath: filepath.Join(dir, "disk.qcow2"), SeedPath: seed, VarsPath: vars,
		ConsoleLog: filepath.Join(dir, "console.log"),
		QMPSock:    socks[0], SerialSock: socks[1], AgentSock: socks[2], StreamSock: socks[3],
		Network:      Network{Mode: NetSlirp, SSHHostPort: sshPort},
		HotplugPorts: 2,
		ShareDir:     share,
		NoShare:      !b.Caps.Virtio9p,
		Accel:        AccelTCG,
		SmbiosSpoof:  true, GPU: true, Balloon: true,
	}
	tc := &Toolchain{System: b.System, Img: b.Img, FirmwareDir: b.FirmwareDir, Caps: b.Caps, Build: &b.BuildIdentity}
	args, err := BuildArgs(tc, &cfg)
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "qemu.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	cmd := exec.Command(b.System, append(args, "-S")...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	fsutil.ConfigureHiddenCmd(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		select {
		case <-exited:
		default:
			cmd.Process.Kill()
			<-exited
		}
		logFile.Close()
	}
	tail := func() string {
		data, _ := os.ReadFile(logPath)
		return strings.TrimSpace(lastLines(data, 3))
	}
	select {
	case err := <-exited:
		logFile.Close()
		return fmt.Errorf("QEMU exited at start (%v): %s", err, tail())
	case <-time.After(3 * time.Second):
	}
	var q *QMP
	for deadline := time.Now().Add(10 * time.Second); ; {
		if q, err = DialQMP(cfg.QMPSock); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		stop()
		return fmt.Errorf("QMP: %v", err)
	}
	fwdPort, err := freePort()
	if err == nil {
		err = q.AddHostForward(HostForward{Proto: "tcp", Port: fwdPort})
	}
	if err == nil {
		var fwds []HostForward
		if fwds, err = q.HostForwards(); err == nil && !containsForward(fwds, fwdPort) {
			err = fmt.Errorf("info usernet does not list the forward to %d", fwdPort)
		}
	}
	if err != nil {
		q.Close()
		stop()
		return fmt.Errorf("host forward over QMP: %v", err)
	}
	q.Execute("quit", nil)
	q.Close()
	select {
	case err := <-exited:
		logFile.Close()
		if err != nil {
			return fmt.Errorf("QEMU did not quit cleanly (%v): %s", err, tail())
		}
	case <-time.After(10 * time.Second):
		stop()
		return fmt.Errorf("QEMU did not quit over QMP")
	}
	return nil
}

func containsForward(fwds []HostForward, port int) bool {
	for _, f := range fwds {
		if f.Proto == "tcp" && f.Port == port {
			return true
		}
	}
	return false
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
