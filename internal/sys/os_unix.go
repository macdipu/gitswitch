//go:build !windows

package sys

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

type Terminal struct {
	In  io.Reader
	Out io.Writer
	f   *os.File
}

func (t *Terminal) Close() { t.f.Close() }

// OpenTerminal opens the controlling Terminal, or returns nil when git runs
// without one (IDE panels, GUI clients, cron).
func OpenTerminal() *Terminal {
	if NoTerminalPrompt() {
		return nil
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	return &Terminal{In: f, Out: f, f: f}
}

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
