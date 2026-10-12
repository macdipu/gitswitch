//go:build !desktop

package window

import (
	"errors"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// Run explains that this build has no window. The desktop app is built
// with `-tags desktop,production`; without them `go build ./...` and
// `go vet ./...` still work without cgo.
func Run(*bootstrap.App) error {
	return errors.New("this gitswitch-app was built without the desktop window (build with -tags desktop,production); run `gitswitch` for the terminal app")
}
