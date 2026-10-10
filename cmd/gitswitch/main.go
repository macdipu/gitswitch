// Command gitswitch: use several git accounts on one computer.
package main

import (
	"os"

	"gitswitch/internal/ui/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:])) }
