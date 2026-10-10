// Package sys holds operating-system details: paths, terminals, starting
// programs, the browser and the clipboard. Layers above it never check
// runtime.GOOS for these concerns.
package sys

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// HomeDir is the user's home folder.
func HomeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ExpandHome turns a leading ~ into the home folder.
func ExpandHome(p string) string {
	if p == "~" {
		return HomeDir()
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(HomeDir(), p[2:])
	}
	return p
}

// ConfigDir is gitswitch's own config folder.
func ConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = filepath.Join(HomeDir(), ".config")
	}
	return filepath.Join(d, "gitswitch")
}

// CacheDir is gitswitch's private cache folder.
func CacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "gitswitch")
}

// RealPath resolves symlinks, or returns p unchanged.
func RealPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// CaseInsensitiveFS reports whether file names ignore case by default.
func CaseInsensitiveFS() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// SamePath compares two paths the way this OS does.
func SamePath(a, b string) bool {
	if CaseInsensitiveFS() {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// NoSystemChanges is set by tests: skip PATH, shortcuts, browser, clipboard
// and popup dialogs.
func NoSystemChanges() bool { return os.Getenv("GITSWITCH_TEST_NO_SYSTEM") != "" }

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

// FS makes file changes, or only prints them in dry-run mode.
type FS struct {
	DryRun bool
	Out    io.Writer
}

// Dry prints what would happen and reports true in dry-run mode.
func (f *FS) Dry(what string) bool {
	if f != nil && f.DryRun {
		out := f.Out
		if out == nil {
			out = os.Stdout
		}
		fmt.Fprintln(out, "[dry-run] "+what)
		return true
	}
	return false
}

func (f *FS) WriteFile(path string, data []byte, perm os.FileMode) error {
	if f.Dry("write " + path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func (f *FS) Remove(path string) error {
	if f.Dry("delete " + path) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *FS) RemoveAll(path string) error {
	if f.Dry("delete " + path) {
		return nil
	}
	return os.RemoveAll(path)
}

func (f *FS) Rename(from, to string) error {
	if f.Dry("rename " + from + " -> " + to) {
		return nil
	}
	return os.Rename(from, to)
}

// FindSSH picks the real ssh program. On Windows the System32 OpenSSH
// client is preferred, because Git for Windows' bundled ssh cannot talk to
// the Windows ssh-agent service (so cached passphrases would not work).
func FindSSH(override string) string {
	if override != "" {
		return ExpandHome(override)
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
