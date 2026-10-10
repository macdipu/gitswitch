//go:build linux || darwin

package sys

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchResize calls redraw when the Terminal window changes size.
func WatchResize(redraw func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				redraw()
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
