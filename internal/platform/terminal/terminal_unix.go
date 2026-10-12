//go:build !windows

package terminal

import (
	"io"
	"os"
)

type Terminal struct {
	In  io.Reader
	Out io.Writer
	f   *os.File
}

func (t *Terminal) Close() { t.f.Close() }

// OpenTerminal opens the controlling Terminal, or returns nil when git runs
// without one (IDE panels, GUI clients, cron) or in a background job.
func OpenTerminal() *Terminal {
	if NoTerminalPrompt() {
		return nil
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if !foreground(f.Fd()) {
		f.Close()
		return nil
	}
	return &Terminal{In: f, Out: f, f: f}
}
