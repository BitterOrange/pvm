//go:build windows

package daemon

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWindowsTaskIsPerUser(t *testing.T) {
	def := windowsTask(`HOST\dev & co`, `C:\Program Files\pvm\pvm.exe`)
	if def[0] != 0xFF || def[1] != 0xFE || len(def)%2 != 0 {
		t.Fatalf("not UTF-16LE with a byte order mark")
	}
	u := make([]uint16, 0, len(def)/2-1)
	for i := 2; i < len(def); i += 2 {
		u = append(u, uint16(def[i])|uint16(def[i+1])<<8)
	}
	text := string(utf16.Decode(u))
	for _, want := range []string{
		`<LogonTrigger><Enabled>true</Enabled><UserId>HOST\dev &amp; co</UserId></LogonTrigger>`,
		`<LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel>`,
		`<Command>C:\Program Files\pvm\pvm.exe</Command><Arguments>daemon start</Arguments>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("task definition lacks %s:\n%s", want, text)
		}
	}
}
