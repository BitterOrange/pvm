//go:build !darwin

package qemu

func checkEntitlement(string) error { return nil }
