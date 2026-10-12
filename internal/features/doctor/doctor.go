// Package doctor checks everything that can make git skip gitswitch or log
// in as the wrong account, and repairs what it can.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	accountinfra "github.com/macdipu/gitswitch/internal/features/account/infrastructure"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	"github.com/macdipu/gitswitch/internal/features/setup"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	sshinfra "github.com/macdipu/gitswitch/internal/features/ssh/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Doctor runs health checks.
type Doctor struct {
	Git       *gitinfra.Runner
	Store     *accountinfra.Store
	Installer *setup.Installer
	Repos     repo.Reader
	Keys      ssh.KeyTool
}

// Check is one health check and its result.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	CanFix bool   `json:"canFix"`
}

// Run performs the health checks. deep=true also scans project folders.
func (d *Doctor) Run(deep bool) []Check {
	var cs []Check
	add := func(id, title string, ok bool, detail string, canFix bool) {
		cs = append(cs, Check{id, title, ok, detail, canFix && !ok})
	}
	gv, err := d.Git.Run("--version")
	add("git", "Git is installed", err == nil, gv, false)

	hook, _ := d.Git.Global("--get", "core.sshCommand")
	dest := setup.InstalledPath()
	_, statErr := os.Stat(dest)
	add("hook", "Git runs through gitswitch", hook == account.HookValue(dest) && statErr == nil,
		"core.sshCommand = "+orNone(hook), true)

	uco, _ := d.Git.Global("--get", "user.useConfigOnly")
	add("useconfigonly", "Commits need a repo email (user.useConfigOnly)", uco == "true",
		"stops commits with a missing or guessed email", true)

	ge, _ := d.Git.Global("--get", "user.email")
	add("globalemail", "No global commit email", ge == "",
		map[bool]string{true: "each repo uses its account's email", false: "global user.email = " + ge + " can cause wrong-account commits"}[ge == ""], true)

	c := d.Store.Load()
	sshBin := process.FindSSH(c.SSHPath)
	_, err = exec.LookPath(sshBin)
	add("ssh", "SSH client found", err == nil, sshBin, false)

	add("accounts", "At least one account", len(c.Accounts) > 0, fmt.Sprintf("%d account(s)", len(c.Accounts)), false)
	for _, a := range c.Accounts {
		add("key-"+a.ID, "Key for "+a.ID, d.Keys.Exists(&a), orNone(a.Key), false)
	}
	// Things that silently bypass gitswitch.
	env := ""
	for _, v := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if os.Getenv(v) != "" {
			env += v + "=" + os.Getenv(v) + " "
		}
	}
	add("env", "No GIT_SSH_COMMAND / GIT_SSH in this environment", env == "",
		map[bool]string{true: "git uses gitswitch", false: "git ignores gitswitch while " + strings.TrimSpace(env) + " is set; remove it from your shell profile or system variables"}[env == ""], false)
	if rc := shellProfilesSettingGitSSH(); len(rc) > 0 {
		add("rcfiles", "Shell profiles don't override git's SSH command", false,
			"GIT_SSH_COMMAND or GIT_SSH is set in "+strings.Join(rc, ", ")+"; remove that line so gitswitch is used", false)
	}
	if wd, err := os.Getwd(); err == nil {
		if gd := d.Git.GitDirFor(wd); gd != "" {
			if v := d.Git.RepoGet(gd, "core.sshCommand"); v != "" && !account.IsHook(v) {
				add("repohook", "This repo uses gitswitch", false, "this repo sets its own core.sshCommand ("+v+"), which overrides gitswitch", true)
			}
		}
	}
	if deep && len(c.ScanFolders)+len(c.Repos) > 0 {
		var over []string
		for _, r := range d.Repos.List(c) {
			if r.SSHOverride != "" {
				over = append(over, filepath.Base(r.Path))
			}
		}
		add("reposhooks", "Scanned repos use gitswitch", len(over) == 0,
			map[bool]string{true: "no repo overrides it", false: fmt.Sprintf("%d repo(s) set their own core.sshCommand: %s", len(over), strings.Join(over, ", "))}[len(over) == 0], true)
	}
	for _, h := range uniqueHosts(c) {
		known := sshinfra.KnownHost(sshBin, h)
		add("knownhost-"+h, "Host key for "+h+" is recorded", known,
			map[bool]string{true: "first connections from IDEs work", false: "run `gitswitch test` once in a terminal to record it"}[known], false)
	}
	if hits := sshConfigIdentityFiles(c); len(hits) > 0 {
		add("sshconfig", "~/.ssh/config doesn't add other keys for your hosts", false,
			"IdentityFile lines for "+strings.Join(hits, ", ")+" are also offered and can log you in as a different account when a key is missing on the host", false)
	}
	if runtime.GOOS == "windows" {
		sc := exec.Command("sc", "query", "ssh-agent")
		process.HideWindow(sc)
		out, err := sc.CombinedOutput()
		running := err == nil && strings.Contains(string(out), "RUNNING")
		add("winagent", "Windows ssh-agent service running", running,
			"Needed to remember key passphrases. Fix (admin PowerShell): Get-Service ssh-agent | Set-Service -StartupType Automatic; Start-Service ssh-agent", false)
	}
	return cs
}

func uniqueHosts(c *account.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range c.Accounts {
		h := remote.CanonicalHost(a.Host)
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func shellProfilesSettingGitSSH() []string {
	var hits []string
	for _, rc := range []string{".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".zshenv", ".config/fish/config.fish"} {
		b, err := os.ReadFile(filepath.Join(filesystem.HomeDir(), rc))
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "#") {
				continue
			}
			if strings.Contains(t, "GIT_SSH_COMMAND") || strings.Contains(t, "GIT_SSH=") || strings.Contains(t, "GIT_SSH ") {
				hits = append(hits, "~/"+rc)
				break
			}
		}
	}
	return hits
}

// sshConfigIdentityFiles finds Host blocks in ~/.ssh/config that match one
// of your account hosts and add an IdentityFile.
func sshConfigIdentityFiles(c *account.Config) []string {
	b, err := os.ReadFile(filepath.Join(filesystem.HomeDir(), ".ssh", "config"))
	if err != nil {
		return nil
	}
	hosts := uniqueHosts(c)
	var hits []string
	var current []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.ReplaceAll(strings.TrimSpace(l), "=", " "))
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "host":
			current = f[1:]
		case "match":
			current = nil
		case "identityfile":
			for _, pat := range current {
				if strings.HasPrefix(pat, "!") {
					continue
				}
				for _, h := range hosts {
					if ok, _ := filepath.Match(strings.ToLower(pat), h); ok && !seen[h+pat] {
						seen[h+pat] = true
						hits = append(hits, "Host "+pat)
					}
				}
			}
		}
	}
	return hits
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

// Fix repairs one check, when it can be repaired automatically.
func (d *Doctor) Fix(id string) error {
	switch id {
	case "hook":
		dest := setup.InstalledPath()
		if _, err := os.Stat(dest); err != nil {
			return d.Installer.Install(d.Installer.Report)
		}
		_, err := d.Git.Global("core.sshCommand", account.HookValue(dest))
		return err
	case "useconfigonly":
		_, err := d.Git.Global("user.useConfigOnly", "true")
		return err
	case "repohook":
		wd, _ := os.Getwd()
		if gd := d.Git.GitDirFor(wd); gd != "" {
			return d.Git.RepoUnset(gd, "core.sshCommand")
		}
		return nil
	case "reposhooks":
		c := d.Store.Load()
		for _, r := range d.Repos.List(c) {
			if r.SSHOverride != "" {
				if err := d.Git.RepoUnset(r.GitDir, "core.sshCommand"); err != nil {
					return err
				}
			}
		}
		return nil
	case "globalemail":
		if e, _ := d.Git.Global("--get", "user.email"); e != "" {
			_, _ = d.Git.Global("gitswitch.backup.email", e)
		}
		_, err := d.Git.Global("--unset", "user.email")
		return err
	}
	return fmt.Errorf("no automatic fix for %s", id)
}
