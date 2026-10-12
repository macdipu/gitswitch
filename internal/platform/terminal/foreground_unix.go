//go:build linux || darwin

package terminal

import (
	"syscall"
	"unsafe"
)

// foreground reports whether this process is in the terminal's foreground
// process group. A background job (`git fetch &`) that reads the terminal
// is stopped by SIGTTIN and would never finish.
func foreground(fd uintptr) bool {
	var pgrp int32
	if err := ioctl(fd, syscall.TIOCGPGRP, unsafe.Pointer(&pgrp)); err != nil {
		return false
	}
	return int(pgrp) == syscall.Getpgrp()
}
