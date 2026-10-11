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

// DetachConsole does nothing: Unix GUI launches have no console window.
func DetachConsole() {}
