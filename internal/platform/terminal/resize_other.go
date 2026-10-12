//go:build !linux && !darwin

package terminal

import (
	"os"
	"os/signal"
)

// On Windows the screen is redrawn at the next key press after a resize.
func WatchResize(redraw func()) (stop func()) { return func() {} }

// WatchQuit calls quit when the program is asked to stop (Ctrl+C or
// Ctrl+Break while a command runs, the console closing).
func WatchQuit(quit func(sig os.Signal)) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				quit(s)
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
