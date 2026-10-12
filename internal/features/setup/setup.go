// Package setup installs gitswitch once (copies the program to a fixed
// folder, points git at it, adds it to PATH and the app menu) and removes it.
package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	accountinfra "github.com/macdipu/gitswitch/internal/features/account/infrastructure"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Installer performs install and uninstall. Report receives progress
// messages for the user; it may be nil.
type Installer struct {
	Git    *gitinfra.Runner
	FS     *filesystem.FS
	Store  *accountinfra.Store
	Report func(string)
}

func (in *Installer) say(a ...any) {
	if in.Report != nil {
		in.Report(strings.TrimSuffix(fmt.Sprintln(a...), "\n"))
	}
}

// ExeName is the program file name on this OS.
func ExeName() string {
	if runtime.GOOS == "windows" {
		return "gitswitch.exe"
	}
	return "gitswitch"
}

// Dir is the fixed per-user folder the program is installed to, so the
// git hook path never changes across updates.
func Dir() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "GitSwitch")
		}
	}
	return filepath.Join(filesystem.HomeDir(), ".local", "bin")
}

// InstalledPath is where the installed program lives.
func InstalledPath() string { return filepath.Join(Dir(), ExeName()) }

// Installed reports whether git currently runs through gitswitch.
func (in *Installer) Installed() bool {
	return in.Git.GlobalGet("core.sshCommand") == account.HookValue(InstalledPath())
}

// Hook returns git's current core.sshCommand.
func (in *Installer) Hook() string { return in.Git.GlobalGet("core.sshCommand") }

// Install copies the binary to a fixed place and points git at it. Because
// the hook lives in the global git config, every terminal and IDE that runs
// git (VS Code, Android Studio, IntelliJ, Windows Terminal, iTerm, ...) uses
// it at once, and keeps using it after restarts and updates. report
// receives progress messages (it becomes the Installer's Report).
func (in *Installer) Install(report func(string)) error {
	in.Report = report
	return in.install()
}

func (in *Installer) install() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is not installed or not on PATH")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	dest := InstalledPath()
	if !in.FS.DryRun {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
	}
	if !samePath(self, dest) && !in.FS.Dry("copy "+self+" -> "+dest) {
		if err := copyExe(self, dest); err != nil {
			return fmt.Errorf("copy to %s: %v", dest, err)
		}
	}
	in.say("Installed", dest)

	prev, _ := in.Git.Global("--get", "core.sshCommand")
	if prev != "" && !account.IsHook(prev) {
		_, _ = in.Git.Global("gitswitch.previousSshCommand", prev)
		in.say("Saved your previous core.sshCommand; `gitswitch uninstall` restores it.")
	}
	if _, err := in.Git.Global("core.sshCommand", account.HookValue(dest)); err != nil {
		return err
	}
	in.say("Git now runs every clone, pull, push and fetch through gitswitch.")

	if v, _ := in.Git.Global("--get", "user.useConfigOnly"); v != "true" {
		_, _ = in.Git.Global("user.useConfigOnly", "true")
		_, _ = in.Git.Global("gitswitch.setUseConfigOnly", "true")
	}
	// A global email makes wrong-account commits silent. Move it aside.
	if email, _ := in.Git.Global("--get", "user.email"); email != "" {
		_, _ = in.Git.Global("gitswitch.backup.email", email)
		_, _ = in.Git.Global("--unset", "user.email")
		if name, _ := in.Git.Global("--get", "user.name"); name != "" {
			_, _ = in.Git.Global("gitswitch.backup.name", name)
		}
		in.say(fmt.Sprintf("Removed global user.email (%s) so commits always use the repo's account.", email))
	}
	if err := in.addToPath(filepath.Dir(dest)); err != nil {
		in.say("Could not add to PATH:", err)
	}
	in.addAppShortcut(dest)
	if runtime.GOOS == "windows" {
		in.say("Tip: run `gitswitch doctor` to check the Windows ssh-agent service.")
	}
	return nil
}

// addAppShortcut adds "GitSwitch" to the Start menu (Windows) or the app
// menu (Linux) so the GUI can be opened without a terminal.
func (in *Installer) addAppShortcut(exe string) {
	if process.NoSystemChanges() || in.FS.Dry("add GitSwitch to the app menu") {
		return
	}
	switch runtime.GOOS {
	case "windows":
		lnk := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "GitSwitch.lnk")
		ps := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut('` + strings.ReplaceAll(lnk, "'", "''") + `'); ` +
			`$s.TargetPath = '` + strings.ReplaceAll(exe, "'", "''") + `'; $s.Arguments = 'ui'; $s.WindowStyle = 7; $s.Description = 'Manage git accounts'; $s.Save()`
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
		process.HideWindow(cmd)
		if cmd.Run() == nil {
			in.say("Added GitSwitch to the Start menu.")
		}
	case "linux":
		dir := filepath.Join(filesystem.HomeDir(), ".local", "share", "applications")
		if os.MkdirAll(dir, 0o755) == nil {
			entry := "[Desktop Entry]\nType=Application\nName=GitSwitch\nComment=Manage git accounts\nExec=" + exe + " ui\nTerminal=false\nCategories=Development;\n"
			if os.WriteFile(filepath.Join(dir, "gitswitch.desktop"), []byte(entry), 0o644) == nil {
				in.say("Added GitSwitch to your applications menu.")
			}
		}
	}
}

// Uninstall gives git its previous settings back. With purge it also
// deletes accounts, rules and identity files (never key files). report
// receives progress messages (it becomes the Installer's Report).
func (in *Installer) Uninstall(purge bool, report func(string)) error {
	in.Report = report
	if v, _ := in.Git.Global("--get", "core.sshCommand"); account.IsHook(v) {
		if prev, _ := in.Git.Global("--get", "gitswitch.previousSshCommand"); prev != "" {
			_, _ = in.Git.Global("core.sshCommand", prev)
		} else {
			_, _ = in.Git.Global("--unset", "core.sshCommand")
		}
	}
	if v, _ := in.Git.Global("--get", "gitswitch.setUseConfigOnly"); v == "true" {
		_, _ = in.Git.Global("--unset", "user.useConfigOnly")
	}
	// Install removed the global email. Put it back only if it is still
	// unset: one the user set since then is theirs to keep.
	if e, _ := in.Git.Global("--get", "gitswitch.backup.email"); e != "" {
		if cur, _ := in.Git.Global("--get", "user.email"); cur == "" {
			_, _ = in.Git.Global("user.email", e)
		} else if cur != e {
			in.say(fmt.Sprintf("Kept your global user.email (%s); the one gitswitch moved aside was %s.", cur, e))
		}
	}
	if n, _ := in.Git.Global("--get", "gitswitch.backup.name"); n != "" {
		if cur, _ := in.Git.Global("--get", "user.name"); cur == "" {
			_, _ = in.Git.Global("user.name", n)
		}
	}
	for _, sec := range []string{"gitswitch.backup", "gitswitch"} {
		if sec == "gitswitch" && !purge {
			continue
		}
		_, _ = in.Git.Global("--remove-section", sec)
	}
	if purge {
		for _, a := range in.Store.Load().Accounts {
			_, _ = in.Git.Global("--remove-section", "gitswitch-account."+a.ID)
		}
		out, _ := in.Git.Run("config", "--global", "--name-only", "--get-regexp", `^includeif\..*\.path$`)
		for _, k := range strings.Split(out, "\n") {
			if k == "" {
				continue
			}
			if v, _ := in.Git.Global("--get", k); strings.Contains(filepath.ToSlash(v), "/gitswitch/accounts/") {
				_, _ = in.Git.Global("--unset", k)
			}
		}
		if !in.FS.Dry("delete " + filesystem.ConfigDir()) {
			_ = os.RemoveAll(filesystem.ConfigDir())
		}
	}
	if !process.NoSystemChanges() {
		switch runtime.GOOS {
		case "windows":
			_ = in.FS.Remove(filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "GitSwitch.lnk"))
		case "linux":
			_ = in.FS.Remove(filepath.Join(filesystem.HomeDir(), ".local", "share", "applications", "gitswitch.desktop"))
		}
	}
	in.say("gitswitch is no longer used by git. You can delete", InstalledPath())
	return nil
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Write the new program next to the old one and rename it into place, so
	// git never runs a half-written hook (it runs on every pull and push).
	out, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".new-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer os.Remove(tmp) // a no-op once renamed
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o755)
	}
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Rename(tmp, dst)
	}
	// A running .exe cannot be overwritten on Windows, but it can be renamed.
	old := dst + ".old"
	moved := false
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil {
			return err
		}
		moved = true
	}
	if err := os.Rename(tmp, dst); err != nil {
		if moved {
			_ = os.Rename(old, dst) // put the previous program back
		}
		return err
	}
	return nil
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if samePath(p, dir) {
			return true
		}
	}
	return false
}

func (in *Installer) addToPath(dir string) error {
	if onPath(dir) || process.NoSystemChanges() {
		return nil
	}
	if in.FS.Dry("add " + dir + " to PATH") {
		return nil
	}
	if runtime.GOOS == "windows" {
		ps := `$d = '` + strings.ReplaceAll(dir, "'", "''") + `'; $p = [Environment]::GetEnvironmentVariable('Path','User'); ` +
			`if (-not $p) { $p = '' }; if (($p -split ';') -notcontains $d) { [Environment]::SetEnvironmentVariable('Path', ($p.TrimEnd(';') + ';' + $d).TrimStart(';'), 'User') }`
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
		process.HideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v %s", err, out)
		}
		in.say("Added", dir, "to your PATH. Open a new terminal to use the `gitswitch` command.")
		return nil
	}
	home := filesystem.HomeDir()
	line := `export PATH="$HOME/.local/bin:$PATH"  # added by gitswitch`
	var wrote []string
	found := false // a profile already adds ~/.local/bin
	// appendLine adds line to a profile; create=false skips a missing file.
	appendLine := func(rc, line string, create bool) {
		p := filepath.Join(home, rc)
		b, err := os.ReadFile(p)
		if err != nil && !create {
			return
		}
		if strings.Contains(string(b), ".local/bin") {
			found = true
			return
		}
		if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
			return
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(f, "\n%s\n", line)
		if cerr := f.Close(); err == nil && cerr == nil {
			wrote = append(wrote, "~/"+filepath.ToSlash(rc))
		}
	}
	if runtime.GOOS == "darwin" {
		appendLine(".zshrc", line, true) // zsh is the default shell
		appendLine(".bash_profile", line, false)
	} else {
		appendLine(".zshrc", line, false)
		appendLine(".bashrc", line, false)
	}
	if st, err := os.Stat(filepath.Join(home, ".config", "fish")); err == nil && st.IsDir() {
		appendLine(filepath.Join(".config", "fish", "conf.d", "gitswitch.fish"), "fish_add_path -g $HOME/.local/bin  # added by gitswitch", true)
	}
	if len(wrote) == 0 && !found {
		appendLine(".profile", line, true) // read by login shells when nothing else is
	}
	switch {
	case len(wrote) > 0:
		in.say("Added ~/.local/bin to your PATH in " + strings.Join(wrote, ", ") + ". Open a new terminal to use the `gitswitch` command.")
	case !found:
		in.say("Could not add ~/.local/bin to your PATH. Add this line to your shell profile:\n  " + line)
	}
	return nil
}
