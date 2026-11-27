//go:build darwin

package native

import "golang.org/x/sys/unix"

func HostMemMB() int {
	v, err := unix.SysctlUint64("hw.memsize")
	if err != nil || v == 0 {
		return 8192
	}
	return int(v >> 20)
}

// smallECAM reports whether QEMU's virt machine must use its low ECAM
// window: its high one needs more guest address space than the
// hypervisor gives. An unknown size counts as small, which only lowers
// the root port count.
func smallECAM() bool {
	for _, name := range []string{"kern.hv.ipa_size_16k", "kern.hv.ipa_size_4k"} {
		if v, err := unix.SysctlUint64(name); err == nil && v > 0 {
			return v < 1<<40
		}
	}
	return true
}
