//go:build darwin

package qemu

import (
	"fmt"
	"strings"
	"time"
)

// checkEntitlement requires a valid signature carrying the hypervisor
// entitlement. Without it macOS refuses HVF, and QEMU would quietly run
// the VM under TCG.
func checkEntitlement(system string) error {
	if out, err := run(20*time.Second, "codesign", "--verify", "--strict", system); err != nil {
		return fmt.Errorf("%s has no valid signature (%s)", system, firstLine(strings.TrimSpace(out)))
	}
	out, err := run(20*time.Second, "codesign", "-d", "--entitlements", ":-", system)
	if err != nil || !strings.Contains(strings.Join(strings.Fields(out), ""), "<key>com.apple.security.hypervisor</key><true/>") {
		return fmt.Errorf("%s lacks the com.apple.security.hypervisor entitlement, so macOS would refuse HVF", system)
	}
	return nil
}
