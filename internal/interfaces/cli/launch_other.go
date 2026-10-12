//go:build !windows

package cli

// startedByDoubleClick is false: macOS and Linux file managers open a
// terminal for a double-clicked program, and there gitswitch asks first.
func startedByDoubleClick() bool { return false }
