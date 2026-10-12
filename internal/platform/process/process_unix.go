//go:build !windows

package process

import (
	"os"
	"os/exec"
	"syscall"
)

// ExecReplace replaces this process with ssh, so the helper uses no memory
// while the transfer runs.
func ExecReplace(bin string, args []string) int {
	path, err := exec.LookPath(bin)
	if err != nil {
		os.Stderr.WriteString("gitswitch: program not found: " + err.Error() + "\n")
		return 255
	}
	err = syscall.Exec(path, append([]string{path}, args...), os.Environ())
	os.Stderr.WriteString("gitswitch: could not start program: " + err.Error() + "\n")
	return 255
}

func HideWindow(cmd *exec.Cmd) {}

// detach starts the program in its own session, away from the terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
