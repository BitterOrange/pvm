//go:build !windows

package qemu

func systemProxy() (string, bool) { return "", false }
