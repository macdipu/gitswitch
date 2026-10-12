// Command gitswitch-app: the GitSwitch desktop app. It is a separate
// program from gitswitch, so the hook git runs on every clone, pull and
// push never loads the window code.
package main

import (
	"os"

	"github.com/macdipu/gitswitch/internal/interfaces/desktop/window"
)

func main() { os.Exit(window.Main(os.Args[1:])) }
