// Package process starts other programs: ssh and its tools, the browser,
// the file manager, an editor and the clipboard, with the console window
// hidden on Windows. Build tags pick the code for each OS.
package process

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// NoSystemChanges is set by tests: skip PATH, shortcuts, browser, clipboard
// and popup dialogs.
func NoSystemChanges() bool { return os.Getenv("GITSWITCH_TEST_NO_SYSTEM") != "" }

// FindSSH picks the real ssh program. On Windows the System32 OpenSSH
// client is preferred, because Git for Windows' bundled ssh cannot talk to
// the Windows ssh-agent service (so cached passphrases would not work).
func FindSSH(override string) string {
	if override != "" {
		return filesystem.ExpandHome(override)
	}
	return FindSSHTool("ssh")
}

// FindSSHTool finds ssh, ssh-add or ssh-keygen.
func FindSSHTool(name string) string {
	if runtime.GOOS == "windows" {
		p := filepath.Join(os.Getenv("SystemRoot"), "System32", "OpenSSH", name+".exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// OpenBrowser opens a web page in the default browser.
func OpenBrowser(url string) error {
	if NoSystemChanges() {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// OpenFolder shows a folder in the file manager.
func OpenFolder(dir string) error {
	if NoSystemChanges() {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// StartProgram starts a command line such as "code --new-window" with arg
// added, and does not wait for it. Words are split on spaces; no quoting.
func StartProgram(command, arg string) error {
	f := strings.Fields(command)
	if len(f) == 0 {
		return errors.New("empty command")
	}
	if NoSystemChanges() {
		return nil
	}
	return exec.Command(f[0], append(f[1:], arg)...).Start()
}

// CopyToClipboard copies text; it reports false when no clipboard tool exists.
func CopyToClipboard(s string) bool {
	if NoSystemChanges() {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
		HideWindow(cmd)
	default:
		for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}} {
			if _, err := exec.LookPath(c[0]); err == nil {
				cmd = exec.Command(c[0], c[1:]...)
				break
			}
		}
	}
	if cmd == nil {
		return false
	}
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run() == nil
}

// StartApp opens the desktop app and does not wait for it. On macOS a
// bundle opens through `open`, so the app gets its Dock icon and focus.
// The app keeps running after the terminal that started it closes.
func StartApp(path, bundle string) error {
	if NoSystemChanges() {
		return nil
	}
	cmd := exec.Command(path)
	if bundle != "" {
		cmd = exec.Command("open", bundle)
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
