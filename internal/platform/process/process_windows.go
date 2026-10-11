//go:build windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	getConsoleProcs = kernel32.NewProc("GetConsoleProcessList")
	freeConsole     = kernel32.NewProc("FreeConsole")
)

// ExecReplace runs ssh with git's stdin/stdout and returns its exit code.
func ExecReplace(bin string, args []string) int {
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		os.Stderr.WriteString("gitswitch: could not start program: " + err.Error() + "\n")
		return 255
	}
	return 0
}

func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// DetachConsole closes the console window when it was opened only for us
// (Start menu shortcut, double-click), not when run from a terminal.
func DetachConsole() {
	var pids [2]uint32
	if n, _, _ := getConsoleProcs.Call(uintptr(unsafe.Pointer(&pids[0])), 2); n == 1 {
		_, _, _ = freeConsole.Call()
	}
}
