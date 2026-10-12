package window

import (
	"fmt"
	"os"

	"github.com/macdipu/gitswitch/internal/bootstrap"
)

// Main is the desktop app program (cmd/gitswitch-app). `--version` prints
// the version without opening the window, so installers can check that
// the app starts on this system.
func Main(args []string) int {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(bootstrap.Version)
		return 0
	}
	if err := Run(bootstrap.New(false, nil)); err != nil {
		fmt.Fprintln(os.Stderr, "gitswitch-app:", err)
		return 1
	}
	return 0
}
