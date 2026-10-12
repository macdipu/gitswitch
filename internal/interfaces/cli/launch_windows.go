//go:build windows

package cli

import (
	"syscall"
	"unsafe"
)

var getConsoleProcessList = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")

// startedByDoubleClick reports a start from Explorer or the Start menu:
// Windows then opens a console for this process alone. From a terminal the
// shell shares the console too.
func startedByDoubleClick() bool {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), 2)
	return n == 1
}
