//go:build !desktop

package desktop

import (
	"errors"

	"github.com/macdipu/gitswitch/internal/app"
)

// Available reports whether this build has the desktop window.
const Available = false

// Run explains that this build has no window. Release builds are made with
// `-tags desktop,production`; plain `go build` leaves the window out so the
// CLI still cross-compiles without cgo.
func Run(*app.App) error {
	return errors.New("this gitswitch was built without the desktop window (build with -tags desktop,production); run `gitswitch` for the terminal app")
}
