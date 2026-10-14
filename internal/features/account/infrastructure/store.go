// Package infrastructure stores accounts, rules and settings in git config
// (see the key list in the account domain package) and writes each
// account's identity file. Import it as accountinfra.
package infrastructure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Store reads and writes accounts, rules and settings in git config.
type Store struct {
	Git *gitinfra.Runner
	FS  *filesystem.FS
}

// Load reads the current configuration (always fresh from git config).
// When git config cannot be read it returns the defaults; LoadChecked also
// says why.
func (s *Store) Load() *account.Config {
	c, _ := s.LoadChecked()
	return c
}

// LoadChecked is Load that reports a git config git cannot read, such as a
// broken ~/.gitconfig. Having no gitswitch settings yet is not an error.
func (s *Store) LoadChecked() (*account.Config, error) {
	// Lists start empty, not nil, so the apps get [] rather than null in JSON.
	c := &account.Config{GUIPrompt: true, PromptTimeout: 60, Accounts: []account.Account{}, Rules: []account.Rule{},
		ScanFolders: []string{}, Repos: []string{}, HiddenRepos: []string{}, Pinned: []string{}, Recent: []string{}}
	accts := map[string]*account.Account{}
	var order []string
	out, err := s.Git.Run("config", "--global", "-z", "--get-regexp", `^gitswitch`)
	if err != nil {
		if gitinfra.ExitCode(err) == 1 { // exit status 1: nothing matched
			return c, nil
		}
		return c, fmt.Errorf("cannot read your git settings: %v", err)
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		k, v, _ := strings.Cut(entry, "\n")
		switch {
		case strings.HasPrefix(k, "gitswitch-account."):
			rest := strings.TrimPrefix(k, "gitswitch-account.")
			i := strings.LastIndex(rest, ".")
			if i < 0 {
				continue
			}
			id, field := rest[:i], rest[i+1:]
			a := accts[id]
			if a == nil {
				a = &account.Account{ID: id}
				accts[id] = a
				order = append(order, id)
			}
			switch field {
			case "host":
				a.Host = v
			case "name":
				a.Name = v
			case "email":
				a.Email = v
			case "key":
				a.Key = v
			case "user":
				a.User = v
			}
		case k == "gitswitch.default":
			c.Default = v
		case k == "gitswitch.guiprompt":
			c.GUIPrompt = account.ParseBool(v, true)
		case k == "gitswitch.prompttimeout":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.PromptTimeout = n
			}
		case k == "gitswitch.sshpath":
			c.SSHPath = v
		case k == "gitswitch.rule":
			if r, ok := account.ParseRule(v); ok {
				c.Rules = append(c.Rules, r)
			}
		case k == "gitswitch.scanfolder":
			c.ScanFolders = append(c.ScanFolders, v)
		case k == "gitswitch.repo":
			c.Repos = append(c.Repos, v)
		case k == "gitswitch.hiderepo":
			c.HiddenRepos = append(c.HiddenRepos, v)
		case k == "gitswitch.pin":
			c.Pinned = append(c.Pinned, v)
		case k == "gitswitch.recent": // the file lists the newest last
			c.Recent = append([]string{v}, c.Recent...)
		case k == "gitswitch.editor":
			c.Editor = v
		case k == "gitswitch.renamed":
			if old, nw, ok := strings.Cut(v, "="); ok {
				if c.Renamed == nil {
					c.Renamed = map[string]string{}
				}
				c.Renamed[strings.ToLower(old)] = nw
			}
		}
	}
	for _, id := range order {
		a := accts[id]
		if a.Host == "" {
			a.Host = "github.com"
		}
		c.Accounts = append(c.Accounts, *a)
	}
	return c, nil
}

// Save creates or updates an account after validating it.
func (s *Store) Save(a account.Account) error {
	if !account.ValidID(a.ID) {
		return fmt.Errorf("account id %q: use letters, digits, - or _ (max 32)", a.ID)
	}
	// A line break would end the value early in the identity file, and the
	// rest of it would be read as more git settings.
	for _, f := range [][2]string{{"host", a.Host}, {"name", a.Name}, {"email", a.Email}, {"key", a.Key}, {"user", a.User}} {
		if strings.IndexFunc(f[1], unicode.IsControl) >= 0 {
			return fmt.Errorf("the %s %q contains a line break or other control character", f[0], f[1])
		}
	}
	if a.Host == "" {
		a.Host = "github.com"
	}
	a.Host = strings.ToLower(strings.TrimSpace(a.Host))
	if strings.ContainsAny(a.Host, " /:@") {
		return fmt.Errorf("host %q: give just the host name, e.g. github.com", a.Host)
	}
	if a.Email != "" && !strings.Contains(a.Email, "@") {
		return fmt.Errorf("%q is not an email address", a.Email)
	}
	if a.Key != "" {
		if err := s.checkKeyNotShared(a); err != nil {
			return err
		}
	}
	sec := "gitswitch-account." + a.ID + "."
	for _, f := range [][2]string{{"host", a.Host}, {"name", a.Name}, {"email", a.Email}, {"key", a.Key}, {"user", a.User}} {
		if f[1] == "" {
			_, _ = s.Git.Global("--unset", sec+f[0])
			continue
		}
		if _, err := s.Git.Global(sec+f[0], f[1]); err != nil {
			return err
		}
	}
	if err := s.writeIdentityFile(a); err != nil {
		return err
	}
	return nil
}

// SuggestedName is the commit name to offer for a new account: the name
// install moved aside, else git's user.name.
func (s *Store) SuggestedName() string {
	if n := s.Git.GlobalGet("gitswitch.backup.name"); n != "" {
		return n
	}
	return s.Git.GlobalGet("user.name")
}

// SetUser records the host username learned from a login test.
func (s *Store) SetUser(id, user string) error {
	_, err := s.Git.Global("gitswitch-account."+id+".user", user)
	return err
}

// checkKeyNotShared refuses a key already used by another account on the
// same host: the host would always log in as that other account.
func (s *Store) checkKeyNotShared(a account.Account) error {
	key := filepath.Clean(filesystem.ExpandHome(a.Key))
	pub, _ := os.ReadFile(key + ".pub")
	for _, o := range s.Load().Accounts {
		if strings.EqualFold(o.ID, a.ID) || o.Key == "" || !remote.SameHost(o.Host, a.Host) {
			continue
		}
		ok := filepath.Clean(filesystem.ExpandHome(o.Key))
		samePath := ok == key || (runtime.GOOS != "linux" && strings.EqualFold(ok, key))
		opub, _ := os.ReadFile(ok + ".pub")
		samePub := len(pub) > 0 && len(opub) > 0 && pubKeyBody(pub) == pubKeyBody(opub)
		if samePath || samePub {
			return fmt.Errorf("that key is already used by account %s on %s; %s would always log in as %s. Create a new key instead", o.ID, o.Host, a.ID, o.ID)
		}
	}
	return nil
}

func pubKeyBody(b []byte) string {
	f := strings.Fields(string(b))
	if len(f) >= 2 {
		return f[0] + " " + f[1]
	}
	return strings.TrimSpace(string(b))
}

// Remove deletes an account, its rules and its identity file. Key files stay.
func (s *Store) Remove(id string) error {
	c := s.Load()
	a := c.Account(id)
	if a == nil {
		return fmt.Errorf("no account %q", id)
	}
	id = a.ID
	if _, err := s.Git.Global("--remove-section", "gitswitch-account."+id); err != nil {
		return err
	}
	for _, r := range c.Rules {
		if strings.EqualFold(r.Account, id) {
			_ = s.RemoveRule(r)
		}
	}
	if strings.EqualFold(c.Default, id) {
		_, _ = s.Git.Global("--unset", "gitswitch.default")
	}
	return s.FS.Remove(IdentityFile(id))
}

// DeleteKeyFiles removes an account's key pair from disk.
func (s *Store) DeleteKeyFiles(a *account.Account) []string {
	if a.Key == "" {
		return nil
	}
	var gone []string
	for _, p := range []string{filesystem.ExpandHome(a.Key), filesystem.ExpandHome(a.Key) + ".pub"} {
		if _, err := os.Stat(p); err == nil && s.FS.Remove(p) == nil {
			gone = append(gone, p)
		}
	}
	return gone
}

func (s *Store) SetDefault(id string) error {
	if id == "" {
		if s.Load().Default == "" {
			return nil
		}
		_, err := s.Git.Global("--unset", "gitswitch.default")
		return err
	}
	a := s.Load().Account(id)
	if a == nil {
		return fmt.Errorf("no account %q", id)
	}
	_, err := s.Git.Global("gitswitch.default", a.ID)
	return err
}

// SetSetting writes gitswitch.<key>.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.Git.Global("gitswitch."+key, value)
	return err
}

// AddRule validates and adds a rule, replacing one with the same pattern.
func (s *Store) AddRule(r account.Rule) error {
	c := s.Load()
	a := c.Account(r.Account)
	if a == nil {
		return fmt.Errorf("no account %q", r.Account)
	}
	r.Account = a.ID
	switch r.Kind {
	case "owner":
		r.Pattern = strings.Trim(strings.TrimSpace(r.Pattern), "/")
		if r.Pattern == "" {
			return errors.New("owner rule needs an owner or org name")
		}
		if strings.ContainsAny(r.Pattern, `~\:`) || strings.HasPrefix(r.Pattern, ".") {
			return fmt.Errorf("%q looks like a folder; choose \"repo is inside folder\" for folder rules", r.Pattern)
		}
	case "folder":
		p, err := filepath.Abs(filesystem.ExpandHome(r.Pattern))
		if err != nil {
			return err
		}
		r.Pattern = filepath.ToSlash(p)
	default:
		return errors.New("rule kind must be owner or folder")
	}
	for _, old := range c.Rules {
		if old.Kind == r.Kind && strings.EqualFold(old.Pattern, r.Pattern) {
			_ = s.RemoveRule(old)
		}
	}
	if _, err := s.Git.Global("--add", "gitswitch.rule", r.String()); err != nil {
		return err
	}
	if r.Kind == "folder" {
		return s.writeFolderIncludes(r.Pattern, r.Account)
	}
	return nil
}

// writeFolderIncludes adds git's native includeIf so repos under the folder
// get the account's name and email even before any network command.
func (s *Store) writeFolderIncludes(folder, id string) error {
	for _, p := range folderVariants(folder) {
		if _, err := s.Git.Global(includeKey(p), filepath.ToSlash(IdentityFile(id))); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RemoveRule(r account.Rule) error {
	_, err := s.Git.Global("--fixed-value", "--unset-all", "gitswitch.rule", r.String())
	if err != nil { // git older than 2.30 has no --fixed-value
		_, err = s.Git.Global("--unset-all", "gitswitch.rule", "^"+regexp.QuoteMeta(r.String())+"$")
	}
	if r.Kind == "folder" {
		for _, p := range folderVariants(r.Pattern) {
			_, _ = s.Git.Global("--unset-all", includeKey(p))
		}
	}
	return err
}

// folderVariants is the folder as given plus its symlink-resolved form.
func folderVariants(p string) []string {
	out := []string{p}
	if rp := filepath.ToSlash(filesystem.RealPath(filepath.FromSlash(p))); rp != p {
		out = append(out, rp)
	}
	return out
}

func includeKey(folder string) string {
	f := strings.TrimSuffix(folder, "/") + "/"
	if filesystem.CaseInsensitiveFS() {
		return "includeIf.gitdir/i:" + f + ".path"
	}
	return "includeIf.gitdir:" + f + ".path"
}

func (s *Store) AddScanFolder(p string) error {
	abs, err := filepath.Abs(filesystem.ExpandHome(p))
	if err != nil {
		return err
	}
	// A typo would otherwise be saved and quietly find no repos.
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return fmt.Errorf("there is no folder %s; check the path, or choose it with Browse", abs)
	}
	for _, f := range s.Load().ScanFolders {
		if f == abs {
			return nil
		}
	}
	_, err = s.Git.Global("--add", "gitswitch.scanFolder", abs)
	return err
}

func (s *Store) RemoveScanFolder(p string) error {
	_, err := s.Git.Global("--fixed-value", "--unset-all", "gitswitch.scanFolder", p)
	return err
}

// Repo lists: key is "repo", "hideRepo", "pin" or "recent". Values are absolute
// working folders; adding one twice or removing a missing one is a no-op.

// AddRepoPath adds a folder to a repo list.
func (s *Store) AddRepoPath(key, p string) error {
	if filesystem.HasPath(s.repoList(key), p) {
		return nil
	}
	_, err := s.Git.Global("--add", "gitswitch."+key, p)
	return err
}

// RemoveRepoPath removes a folder from a repo list, in any spelling the
// file system treats as the same folder.
func (s *Store) RemoveRepoPath(key, p string) error {
	for _, v := range s.repoList(key) {
		if filesystem.SamePath(v, p) {
			if _, err := s.Git.Global("--fixed-value", "--unset-all", "gitswitch."+key, v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) repoList(key string) []string {
	c := s.Load()
	return map[string][]string{"repo": c.Repos, "hideRepo": c.HiddenRepos, "pin": c.Pinned, "recent": c.Recent}[key]
}

// Rename renames an account's stored settings, rules, default, identity
// file and generated key files, and records the rename so repos that still
// name the old id keep working.
func (s *Store) Rename(oldID, newID string) error {
	c := s.Load()
	a := c.Account(oldID)
	if a == nil {
		return fmt.Errorf("no account %q", oldID)
	}
	oldID = a.ID
	if !account.ValidID(newID) {
		return fmt.Errorf("account id %q: use letters, digits, - or _ (max 32)", newID)
	}
	if strings.EqualFold(oldID, newID) {
		return nil
	}
	for _, o := range c.Accounts {
		if strings.EqualFold(o.ID, newID) {
			return fmt.Errorf("an account called %s already exists", newID)
		}
	}
	if _, err := s.Git.Global("--rename-section", "gitswitch-account."+oldID, "gitswitch-account."+newID); err != nil {
		return err
	}
	// From here on each step is tried even when an earlier one failed, and
	// every failure is reported. The rename record comes first: with it,
	// anything still naming the old id keeps finding the account.
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	_, err := s.Git.Global("--add", "gitswitch.renamed", oldID+"="+newID)
	keep(err)
	if a.Key != "" && filepath.Clean(filesystem.ExpandHome(a.Key)) == filepath.Clean(DefaultKeyPath(oldID)) {
		ok, nk := DefaultKeyPath(oldID), DefaultKeyPath(newID)
		_, errOld := os.Stat(ok)
		if _, errNew := os.Stat(nk); errOld == nil && os.IsNotExist(errNew) {
			if err := s.FS.Rename(ok, nk); err != nil {
				keep(fmt.Errorf("move the key file: %w", err))
			} else {
				if _, err := os.Stat(ok + ".pub"); err == nil {
					keep(s.FS.Rename(ok+".pub", nk+".pub"))
				}
				_, err := s.Git.Global("gitswitch-account."+newID+".key", nk)
				keep(err)
			}
		}
	}
	// Write the new identity file before pointing anything at it.
	oldFile, newFile := IdentityFile(oldID), IdentityFile(newID)
	na := *a
	na.ID = newID
	if err := s.writeIdentityFile(na); err != nil {
		keep(fmt.Errorf("write the identity file: %w", err))
	} else {
		out, _ := s.Git.Run("config", "--global", "--get-regexp", `^includeif\..*\.path$`)
		for _, line := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(line, " ")
			if ok && filepath.ToSlash(v) == filepath.ToSlash(oldFile) {
				_, err := s.Git.Global(k, filepath.ToSlash(newFile))
				keep(err)
			}
		}
	}
	for _, r := range c.Rules {
		if !strings.EqualFold(r.Account, oldID) {
			continue
		}
		// Add the new rule before removing the old one, so a failure never
		// leaves the folder or owner without a rule.
		nr := r
		nr.Account = newID
		if _, err := s.Git.Global("--add", "gitswitch.rule", nr.String()); err != nil {
			keep(fmt.Errorf("move the rule %s: %w", r, err))
			continue
		}
		keep(s.RemoveRule(r))
		if r.Kind == "folder" { // RemoveRule also removed the folder's includeIf
			keep(s.writeFolderIncludes(r.Pattern, newID))
		}
	}
	if strings.EqualFold(c.Default, oldID) {
		_, err := s.Git.Global("gitswitch.default", newID)
		keep(err)
	}
	if len(errs) == 0 {
		if _, err := os.Stat(oldFile); err == nil {
			keep(s.FS.Remove(oldFile))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("renamed %s to %s, but some settings could not be updated: %w", oldID, newID, errors.Join(errs...))
	}
	return nil
}

func (s *Store) writeIdentityFile(a account.Account) error {
	var b strings.Builder
	b.WriteString("# Written by gitswitch. Edit the account with `gitswitch account edit`.\n[user]\n")
	if a.Name != "" {
		fmt.Fprintf(&b, "\tname = %s\n", gitQuote(a.Name))
	}
	if a.Email != "" {
		fmt.Fprintf(&b, "\temail = %s\n", gitQuote(a.Email))
	}
	return s.FS.WriteFile(IdentityFile(a.ID), []byte(b.String()), 0o644)
}

func gitQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// ---- per-repo ----

// ApplyIdentity sets the repo's user.name and user.email to the account's.
func (s *Store) ApplyIdentity(gitDir string, a *account.Account) {
	if gitDir == "" || a == nil {
		return
	}
	if a.Email != "" && s.Git.RepoGet(gitDir, "user.email") != a.Email {
		_ = s.Git.RepoSet(gitDir, "user.email", a.Email)
	}
	if a.Name != "" && s.Git.RepoGet(gitDir, "user.name") != a.Name {
		_ = s.Git.RepoSet(gitDir, "user.name", a.Name)
	}
}

// SetRepoAccount saves the account for a repo, applies its identity, and
// removes a repo-level core.sshCommand that would bypass gitswitch.
// It returns the removed value, if any.
func (s *Store) SetRepoAccount(gitDir string, a *account.Account) (string, error) {
	if err := s.Git.RepoSet(gitDir, "gitswitch.account", a.ID); err != nil {
		return "", err
	}
	s.ApplyIdentity(gitDir, a)
	if v := s.Git.RepoGet(gitDir, "core.sshCommand"); v != "" && !account.IsHook(v) {
		return v, s.Git.RepoUnset(gitDir, "core.sshCommand")
	}
	return "", nil
}

// SaveRepoAccountID changes only the account id saved for a repo.
func (s *Store) SaveRepoAccountID(gitDir, id string) error {
	return s.Git.RepoSet(gitDir, "gitswitch.account", id)
}

// ForgetRepo removes a repo's saved account.
func (s *Store) ForgetRepo(gitDir string) error {
	return s.Git.RepoUnset(gitDir, "gitswitch.account")
}
