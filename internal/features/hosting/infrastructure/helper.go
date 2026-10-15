package infrastructure

import (
	"os"
	"path/filepath"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	repoinfra "github.com/macdipu/gitswitch/internal/features/repository/infrastructure"
)

// HelperConfig keeps GitSwitch's credential helper sections in the global
// git config. It implements the hosting domain's HelperConfig.
type HelperConfig struct {
	Git *gitinfra.Runner
}

func (h *HelperConfig) Hook() string { return h.Git.GlobalGet("core.sshCommand") }

// Entries reads credential.helper and credential.<URL>.helper from the
// system config, then the global one, in file order.
func (h *HelperConfig) Entries() ([]hosting.HelperEntry, error) {
	var out []hosting.HelperEntry
	for _, scope := range []string{"--system", "--global"} {
		// Exit code 1 means nothing matched; a missing file is fine too.
		s, _ := h.Git.Run("config", scope, "-z", "--get-regexp", `^credential\..*helper$`)
		out = append(out, ParseHelperEntries(s)...)
	}
	return out, nil
}

// ParseHelperEntries reads `git config -z --get-regexp` output: each entry
// is "key\nvalue\x00", or "key\x00" for a key with no value.
func ParseHelperEntries(s string) []hosting.HelperEntry {
	var out []hosting.HelperEntry
	for _, rec := range strings.Split(s, "\x00") {
		if rec == "" {
			continue
		}
		key, val, _ := strings.Cut(rec, "\n")
		key = strings.TrimPrefix(key, "credential.")
		url := ""
		if key != "helper" {
			var ok bool
			if url, ok = strings.CutSuffix(key, ".helper"); !ok {
				continue
			}
		}
		out = append(out, hosting.HelperEntry{URL: url, Value: val})
	}
	return out
}

func (h *HelperConfig) Write(host string, values []string) error {
	if err := h.Remove(host); err != nil {
		return err
	}
	key := hosting.HelperSection(host) + ".helper"
	for _, v := range values {
		if _, err := h.Git.Global("--add", key, v); err != nil {
			return err
		}
	}
	return nil
}

func (h *HelperConfig) Remove(host string) error {
	key := hosting.HelperSection(host) + ".helper"
	if v, _ := h.Git.Global("--get-all", key); v == "" {
		return nil
	}
	_, err := h.Git.Global("--remove-section", hosting.HelperSection(host))
	return err
}

// HelperRepos finds the repo git runs the credential helper for. It
// implements the hosting domain's Repos.
type HelperRepos struct {
	Git    *gitinfra.Runner
	Reader *repoinfra.Reader
}

// Current is GIT_DIR, which git sets while cloning and in many commands,
// else the repo of the folder the helper runs in.
func (h *HelperRepos) Current() string {
	if d := os.Getenv("GIT_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			if wd, err := os.Getwd(); err == nil {
				d = filepath.Join(wd, d)
			}
		}
		out, err := h.Git.Run("--git-dir="+d, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return ""
		}
		return out
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return h.Git.GitDirFor(wd)
}

func (h *HelperRepos) AccountFor(c *account.Config, gitDir string) *account.Account {
	a, _ := h.Reader.AccountFor(c, gitDir)
	return a
}
