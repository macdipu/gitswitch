//go:build linux || darwin

package terminal

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchResize calls redraw when the Terminal window changes size.
func WatchResize(redraw func()) (stop func()) {
	return watch(func(os.Signal) { redraw() }, syscall.SIGWINCH)
}

// WatchQuit calls quit when the program is asked to stop (kill, the
// Terminal window closing, Ctrl+C while a command runs in cooked mode), so
// a full-screen app can give the Terminal back as it was.
func WatchQuit(quit func(sig os.Signal)) (stop func()) {
	return watch(quit, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
}

func watch(f func(os.Signal), sigs ...os.Signal) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				f(s)
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
