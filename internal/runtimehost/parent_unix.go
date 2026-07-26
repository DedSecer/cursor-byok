//go:build !windows

package runtimehost

import "syscall"

func ParentProcessAlive(pid int) bool {
	if pid <= 0 {
		return true
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
