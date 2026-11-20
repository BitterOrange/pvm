//go:build windows

package daemon

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/paths"
)

const windowsTaskName = "pvm-daemon"

// windowsTask is the task definition: run `pvm daemon start` when this
// user logs on. `schtasks /SC ONLOGON` means any user's logon, which
// needs an administrator; a logon trigger and principal limited to the
// current user do not. The task starts the daemon hidden and detached
// through `daemon start` and exits, instead of running `daemon run` in a
// console window that would stay open for the whole session.
func windowsTask(userID, exe string) []byte {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	text := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>` + esc(userID) + `</UserId></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author"><UserId>` + esc(userID) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
  </Settings>
  <Actions Context="Author">
    <Exec><Command>` + esc(exe) + `</Command><Arguments>daemon start</Arguments></Exec>
  </Actions>
</Task>
`
	// schtasks reads the file as UTF-16, as the declaration says
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, u := range utf16.Encode([]rune(text)) {
		buf.Write([]byte{byte(u), byte(u >> 8)})
	}
	return buf.Bytes()
}

// schtasks writes its reason ("Access is denied.") on a line of its own;
// one line keeps it next to the error wherever only the first line shows.
func schtasksErr(what string, err error, out []byte) error {
	return fmt.Errorf("schtasks %s: %v: %s", what, err, strings.Join(strings.Fields(string(out)), " "))
}

func ServiceInstall(p *paths.Paths) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	u, err := user.Current()
	if err != nil {
		return err
	}
	def := windowsTask(u.Username, exe)
	f, err := os.CreateTemp("", "pvm-daemon-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(def)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	cmd := exec.Command("schtasks", "/Create", "/F", "/TN", windowsTaskName, "/XML", f.Name())
	fsutil.ConfigureHiddenCmd(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return schtasksErr("create", err, out)
	}
	runCmd := exec.Command("schtasks", "/Run", "/TN", windowsTaskName)
	fsutil.ConfigureHiddenCmd(runCmd)
	_ = runCmd.Run()
	return nil
}

func ServiceUninstall(p *paths.Paths) error {
	cmd := exec.Command("schtasks", "/Delete", "/F", "/TN", windowsTaskName)
	fsutil.ConfigureHiddenCmd(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return schtasksErr("delete", err, out)
	}
	return nil
}
