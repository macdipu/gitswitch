//go:build !linux && !darwin && !windows

package terminal

import "errors"

type TTYState struct{}

func MakeRaw() (*TTYState, error) {
	return nil, errors.New("the TUI is not supported on this system; use `gitswitch ui`")
}
func RestoreTTY(*TTYState) {}
func TermSize() (int, int) { return 80, 24 }
