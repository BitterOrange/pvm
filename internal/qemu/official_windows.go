//go:build windows

package qemu

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// DetectOfficial finds QEMU from MSYS2 UCRT64, the build qemu.org links to
// that installs without administrator rights. The pacman package entry
// shows MSYS2 installed it.
func DetectOfficial() (*Official, error) {
	root := msys2Root()
	bin := filepath.Join(root, "ucrt64", "bin")
	system := filepath.Join(bin, "qemu-system-x86_64.exe")
	if _, err := os.Stat(system); err != nil {
		return nil, officialMissing("QEMU from MSYS2 UCRT64 is not installed in %s; in a UCRT64 shell run pacman -S mingw-w64-ucrt-x86_64-qemu (see https://www.qemu.org/download/#windows), then pvm qemu use official", root)
	}
	pkgs, _ := filepath.Glob(filepath.Join(root, "var", "lib", "pacman", "local", "mingw-w64-ucrt-x86_64-qemu-[0-9]*"))
	if len(pkgs) == 0 {
		return nil, officialMissing("%s is not installed by MSYS2's pacman; use pvm qemu install", system)
	}
	version := strings.TrimPrefix(filepath.Base(pkgs[0]), "mingw-w64-ucrt-x86_64-qemu-")
	return &Official{
		Channel: "msys2", System: system, SystemExec: system,
		Img:            filepath.Join(bin, "qemu-img.exe"),
		FirmwareDir:    filepath.Join(root, "ucrt64", "share", "qemu"),
		PackageVersion: version,
	}, nil
}

// msys2Root reads the MSYS2 installer's uninstall entry, else the default.
func msys2Root() string {
	for _, hive := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(hive, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.ENUMERATE_SUB_KEYS|registry.READ|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			sk, err := registry.OpenKey(hive, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+n, registry.READ|registry.WOW64_64KEY)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			loc, _, _ := sk.GetStringValue("InstallLocation")
			sk.Close()
			if strings.HasPrefix(name, "MSYS2") && loc != "" {
				return loc
			}
		}
	}
	return `C:\msys64`
}
