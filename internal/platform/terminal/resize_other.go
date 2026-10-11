//go:build !linux && !darwin

package terminal

// On Windows the screen is redrawn at the next key press after a resize.
func WatchResize(redraw func()) (stop func()) { return func() {} }
