//go:build windows

package qemu

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func checkFreeSpace(dir string, need int64) error {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return nil
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return nil // unknown; the extraction fails clearly if space runs out
	}
	if int64(free) < need {
		return fmt.Errorf("%s has %d MB free; installing needs about %d MB", dir, free>>20, need>>20)
	}
	return nil
}
