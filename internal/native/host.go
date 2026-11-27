package native

import "runtime"

// A busy guest with many logical CPUs starves the host's own UI and I/O
// threads, so default to a quarter (rounded up). Overcommit across VMs is fine.
func DefaultCPUs() int { return (runtime.NumCPU() + 3) / 4 }

// WHPX commits all guest RAM at start, so large defaults fail on hosts with
// little free commit; a quarter also avoids swapping during parallel builds.
func DefaultMemMB() int {
	mb := HostMemMB() / 4
	if mb < 2048 {
		mb = 2048
	}
	return mb
}

// PCIRootPorts is how many PCIe root ports a VM on this host can have.
// Every root port takes a PCI bus, and QEMU's virt machine on an Apple
// hypervisor with a small guest address space (the M1's is 36 bits) has to
// keep its ECAM window below 4 GiB, where it holds 16 buses: bus 0 and 15
// root ports. Elsewhere the ECAM window has 256 buses.
func PCIRootPorts() int {
	if smallECAM() {
		return 15
	}
	return 64
}
