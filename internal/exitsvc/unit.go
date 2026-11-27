package exitsvc

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/huangzheng2016/pvm/internal/exit"
)

// kernelNATCaps are what kernel NAT needs without root: TUN devices, ip
// addresses, nftables (CAP_NET_ADMIN) and iptables' raw sockets
// (CAP_NET_RAW). Forwarding is enabled by the installer through sysctl.d.
const kernelNATCaps = "CAP_NET_ADMIN CAP_NET_RAW"

// SysctlPath enables IPv4 forwarding for kernel NAT at boot.
const SysctlPath = "/etc/sysctl.d/60-pvm-exit.conf"

// SysctlContent is SysctlPath's content.
const SysctlContent = "# written by pvm-exit: kernel NAT forwards tunnel traffic\nnet.ipv4.ip_forward=1\n"

// systemdQuote quotes one ExecStart argument.
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// SystemUnit runs exe as the pvm-exit user. Only kernel NAT gets network
// capabilities; userspace NAT runs with none.
func SystemUnit(exe, configPath, nat string) string {
	caps := kernelNATCaps
	if nat == exit.NATUserspace {
		caps = ""
	}
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=pvm exit server\nAfter=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	fmt.Fprintf(&b, "User=%s\nGroup=%s\n", ServiceUser, ServiceUser)
	fmt.Fprintf(&b, "ExecStart=%s serve --config %s\n", systemdQuote(exe), systemdQuote(configPath))
	b.WriteString("Restart=always\nRestartSec=5\n")
	fmt.Fprintf(&b, "AmbientCapabilities=%s\nCapabilityBoundingSet=%s\n", caps, caps)
	b.WriteString("NoNewPrivileges=yes\nProtectSystem=full\nProtectHome=yes\nPrivateTmp=yes\n")
	b.WriteString("StateDirectory=pvm-exit\nStateDirectoryMode=0700\n\n")
	b.WriteString("[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// UserUnit runs exe as a systemd user service.
func UserUnit(exe, configPath string) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=pvm exit server (user)\n\n")
	b.WriteString("[Service]\nType=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s serve --config %s\n", systemdQuote(exe), systemdQuote(configPath))
	b.WriteString("Restart=always\nRestartSec=5\nNoNewPrivileges=yes\n\n")
	b.WriteString("[Install]\nWantedBy=default.target\n")
	return b.String()
}

// pingGroupAllows reports whether gid may open unprivileged ICMP sockets,
// which userspace NAT uses to forward client pings.
func pingGroupAllows(gid int) bool {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ping_group_range")
	if err != nil {
		return false
	}
	return pingRangeAllows(string(b), gid)
}

func pingRangeAllows(s string, gid int) bool {
	f := strings.Fields(s)
	if len(f) != 2 {
		return false
	}
	lo, err1 := strconv.Atoi(f[0])
	hi, err2 := strconv.Atoi(f[1])
	return err1 == nil && err2 == nil && lo <= gid && gid <= hi
}

// PackagedUnitPath is where the .deb installs the system unit.
const PackagedUnitPath = "/usr/lib/systemd/system/" + unitName

// PackagedNATDropIn takes the network capabilities away from the
// package's unit, which grants them for kernel NAT, when the config uses
// userspace NAT.
const PackagedNATDropIn = "/etc/systemd/system/" + unitName + ".d/10-userspace-nat.conf"

// PackagedNATDropInContent empties both capability sets.
const PackagedNATDropInContent = "# written by pvm-exit init: userspace NAT needs no capabilities\n[Service]\nAmbientCapabilities=\nCapabilityBoundingSet=\n"
