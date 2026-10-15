// Package setup installs gitswitch once (copies the program to a fixed
// folder, points git at it, adds it to PATH and the app menu) and removes it.
package setup

import (
	"errors"
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
	hostinginfra "github.com/macdipu/gitswitch/internal/features/hosting/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Installer performs install and uninstall. Report receives progress
// messages for the user; it may be nil. Version is written into the macOS
// app bundle.
type Installer struct {
	Git     *gitinfra.Runner
	FS      *filesystem.FS
	Store   *accountinfra.Store
	Version string
	Report  func(string)
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

// AppExeName is the desktop app's file name on this OS. It is not
// "GitSwitch.exe": Windows file names ignore case, so that would be the
// same file as gitswitch.exe.
func AppExeName() string {
	if runtime.GOOS == "windows" {
		return "gitswitch-app.exe"
	}
	return "gitswitch-app"
}

// AppBundle is the macOS app bundle the desktop app is installed in, and
// "" on other systems.
func AppBundle() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	return filepath.Join(filesystem.HomeDir(), "Applications", "GitSwitch.app")
}

// AppPath is where the installed desktop app lives. The hook program and
// the app are separate files, so git never loads the window code.
func AppPath() string {
	if b := AppBundle(); b != "" {
		return filepath.Join(b, "Contents", "MacOS", AppExeName())
	}
	return filepath.Join(Dir(), AppExeName())
}

// AppInstalled reports whether the desktop app is installed.
func AppInstalled() bool {
	_, err := os.Stat(AppPath())
	return err == nil
}

// ErrNoApp is returned by OpenApp when the desktop app is not installed.
var ErrNoApp = errors.New("the desktop app is not installed on this computer (it needs a desktop, and on Linux WebKitGTK 4.1). Run `gitswitch` for the terminal app, or run the installer again to add the desktop app")

// OpenApp starts the desktop app without waiting for it. A running app
// comes to the front instead of opening a second window. Without an
// installed app it tries gitswitch-app next to this program (a dev build).
func OpenApp() error {
	if AppInstalled() {
		return process.StartApp(AppPath(), AppBundle())
	}
	if self, err := os.Executable(); err == nil {
		dev := filepath.Join(filepath.Dir(self), AppExeName())
		if _, err := os.Stat(dev); err == nil {
			return process.StartApp(dev, "")
		}
	}
	return ErrNoApp
}

// hookSource is the program to install as the git hook. Run from the
// desktop app (its install button), that is gitswitch next to the app, or
// the one already installed.
func hookSource(self string) (string, error) {
	if !samePath(filepath.Base(self), AppExeName()) {
		return self, nil
	}
	for _, p := range []string{filepath.Join(filepath.Dir(self), ExeName()), InstalledPath()} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("the gitswitch program is missing (looked next to %s and at %s); run the installer again", self, InstalledPath())
}

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
	src, err := hookSource(self)
	if err != nil {
		return err
	}
	dest := InstalledPath()
	if !in.FS.DryRun {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
	}
	if !samePath(src, dest) && !in.FS.Dry("copy "+src+" -> "+dest) {
		if err := copyExe(src, dest); err != nil {
			return fmt.Errorf("copy to %s: %v", dest, err)
		}
	}
	in.say("Installed", dest)
	if err := in.installApp(self, src); err != nil {
		in.say("Could not install the desktop app:", err)
	}

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
	if AppInstalled() {
		in.addAppShortcut(AppPath())
	}
	if runtime.GOOS == "windows" {
		in.say("Tip: run `gitswitch doctor` to check the Windows ssh-agent service.")
	}
	return nil
}

// installApp copies the desktop app to AppPath when one came with the
// download: the running program itself, or gitswitch-app next to the hook
// program. Servers and `go install` have none, and keep the terminal app.
func (in *Installer) installApp(self, hookSrc string) error {
	src := filepath.Join(filepath.Dir(hookSrc), AppExeName())
	if samePath(filepath.Base(self), AppExeName()) {
		src = self
	}
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	dest := AppPath()
	if samePath(src, dest) {
		return nil
	}
	if in.FS.Dry("copy " + src + " -> " + dest) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := copyExe(src, dest); err != nil {
		return err
	}
	if b := AppBundle(); b != "" {
		if err := filesystem.WriteAtomic(filepath.Join(b, "Contents", "Info.plist"), []byte(infoPlist(in.Version)), 0o644); err != nil {
			return err
		}
	}
	in.say("Installed the desktop app:", map[bool]string{true: AppBundle(), false: dest}[AppBundle() != ""])
	return nil
}

// infoPlist describes the macOS app bundle, so GitSwitch shows up in
// Launchpad, Spotlight and the Dock.
func infoPlist(version string) string {
	v := strings.TrimPrefix(version, "v")
	if v == "" || v == "dev" {
		v = "0.0.0"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>GitSwitch</string>
	<key>CFBundleDisplayName</key><string>GitSwitch</string>
	<key>CFBundleIdentifier</key><string>com.macdipu.gitswitch</string>
	<key>CFBundleExecutable</key><string>` + AppExeName() + `</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>` + v + `</string>
	<key>CFBundleVersion</key><string>` + v + `</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
`
}

// addAppShortcut adds "GitSwitch" to the Start menu (Windows) or the app
// menu (Linux), opening the desktop app. macOS needs none: the app bundle
// in ~/Applications is found by Launchpad and Spotlight.
func (in *Installer) addAppShortcut(app string) {
	if process.NoSystemChanges() || in.FS.Dry("add GitSwitch to the app menu") {
		return
	}
	switch runtime.GOOS {
	case "windows":
		lnk := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "GitSwitch.lnk")
		ps := `$s = (New-Object -ComObject WScript.Shell).CreateShortcut('` + strings.ReplaceAll(lnk, "'", "''") + `'); ` +
			`$s.TargetPath = '` + strings.ReplaceAll(app, "'", "''") + `'; $s.Description = 'Manage git accounts'; $s.Save()`
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
		process.HideWindow(cmd)
		if cmd.Run() == nil {
			in.say("Added GitSwitch to the Start menu.")
		}
	case "linux":
		dir := filepath.Join(filesystem.HomeDir(), ".local", "share", "applications")
		if os.MkdirAll(dir, 0o755) == nil {
			entry := "[Desktop Entry]\nType=Application\nName=GitSwitch\nComment=Manage git accounts\nExec=" + desktopQuote(app) + "\nTerminal=false\nCategories=Development;\n"
			if os.WriteFile(filepath.Join(dir, "gitswitch.desktop"), []byte(entry), 0o644) == nil {
				in.say("Added GitSwitch to your applications menu.")
			}
		}
	}
}

// desktopQuote quotes a path for the Exec line of a .desktop file, so a
// home folder with spaces still works.
func desktopQuote(p string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`)
	return `"` + r.Replace(p) + `"`
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
	in.removeCredentialHelpers()
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
		secrets := hostinginfra.NewSecrets()
		for _, a := range in.Store.Load().Accounts {
			_, _ = in.Git.Global("--remove-section", "gitswitch-account."+a.ID)
			if !in.FS.Dry("remove the GitHub token of " + a.ID + " from the keychain") {
				_ = secrets.Delete(a.ID) // none saved is fine
			}
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
	if b := AppBundle(); b != "" {
		_ = in.FS.RemoveAll(b)
	} else {
		_ = in.FS.Remove(AppPath())
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

// removeCredentialHelpers deletes the credential.https://<host>/ sections
// GitSwitch wrote for signed-in accounts (see the hosting feature), so git
// asks only your own credential helpers again.
func (in *Installer) removeCredentialHelpers() {
	out, _ := in.Git.Global("-z", "--get-regexp", `^credential\..*/\.helper$`)
	done := map[string]bool{}
	for _, rec := range strings.Split(out, "\x00") {
		key, val, _ := strings.Cut(rec, "\n")
		section, ok := strings.CutSuffix(key, ".helper")
		if !ok || done[section] || !strings.Contains(strings.ToLower(val), "gitswitch") || !strings.HasSuffix(val, " credential") {
			continue
		}
		done[section] = true
		if _, err := in.Git.Global("--remove-section", section); err == nil {
			in.say("Removed GitSwitch's HTTPS credential helper (" + section + ").")
		}
	}
}
