// Package terminal is the user's terminal: opening it directly (git owns
// stdin and stdout during the ssh hook), raw keyboard input, window size
// and resize events. Build tags pick the code for each OS.
package terminal

import "os"

// NoTerminalPrompt reports git runs started by an IDE's source-control
// panel (or explicitly asked not to prompt), where nobody sees a terminal.
func NoTerminalPrompt() bool {
	// VS Code sets these for git runs from both its panel and its integrated
	// terminal; the integrated terminal also sets TERM_PROGRAM=vscode.
	if os.Getenv("VSCODE_GIT_ASKPASS_MAIN") != "" && os.Getenv("TERM_PROGRAM") != "vscode" {
		return true
	}
	return os.Getenv("GITSWITCH_NO_PROMPT") != ""
}
