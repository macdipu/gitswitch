//go:build windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
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

// detach starts the program without our console (DETACHED_PROCESS) and
// outside our Ctrl+C group (CREATE_NEW_PROCESS_GROUP).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
