//go:build !windows

package qemu

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func checkFreeSpace(dir string, need int64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return nil // unknown; the extraction fails clearly if space runs out
	}
	if free := int64(st.Bavail) * int64(st.Bsize); free < need {
		return fmt.Errorf("%s has %d MB free; installing needs about %d MB", dir, free>>20, need>>20)
	}
	return nil
}
