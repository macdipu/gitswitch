//go:build windows

package sys

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
)

type Terminal struct {
	In  io.Reader
	Out io.Writer
	fi  *os.File
	fo  *os.File
}

func (t *Terminal) Close() { t.fi.Close(); t.fo.Close() }

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindow = kernel32.NewProc("GetConsoleWindow")
)

// OpenTerminal opens the console directly. When git was started by an IDE
// or GUI client with no console (CREATE_NO_WINDOW), GetConsoleWindow returns
// 0 and we never prompt. ConPTY terminals (Windows Terminal, VS Code,
// Android Studio) return a handle, so prompting works there.
func OpenTerminal() *Terminal {
	if NoTerminalPrompt() {
		return nil
	}
	if h, _, _ := getConsoleWindow.Call(); h == 0 {
		return nil
	}
	fi, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	fo, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		fi.Close()
		return nil
	}
	return &Terminal{In: fi, Out: fo, fi: fi, fo: fo}
}

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
