//go:build !linux && !darwin && !windows

package terminal

func foreground(fd uintptr) bool { return true }
