package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/macdipu/gitswitch/internal/gitcli"
	"github.com/macdipu/gitswitch/internal/sys"
)

// Store reads and writes accounts, rules and settings in git config.
type Store struct {
	Git *gitcli.Runner
	FS  *sys.FS
}

// Load reads the current configuration (always fresh from git config).
func (s *Store) Load() *Config {
	c := &Config{GUIPrompt: true, PromptTimeout: 60}
	accts := map[string]*Account{}
	var order []string
	out, err := s.Git.Run("config", "--global", "-z", "--get-regexp", `^gitswitch`)
	if err != nil && out == "" {
		return c
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
				a = &Account{ID: id}
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
			c.GUIPrompt = ParseBool(v, true)
		case k == "gitswitch.prompttimeout":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.PromptTimeout = n
			}
		case k == "gitswitch.sshpath":
			c.SSHPath = v
		case k == "gitswitch.rule":
			if r, ok := ParseRule(v); ok {
				c.Rules = append(c.Rules, r)
			}
		case k == "gitswitch.scanfolder":
			c.ScanFolders = append(c.ScanFolders, v)
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
	return c
}

// Save creates or updates an account after validating it.
func (s *Store) Save(a Account) error {
	if !ValidID(a.ID) {
		return fmt.Errorf("account id %q: use letters, digits, - or _ (max 32)", a.ID)
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
	if s.Load().Default == "" {
		_, _ = s.Git.Global("gitswitch.default", a.ID)
	}
	return nil
}

// SetUser records the host username learned from a login test.
func (s *Store) SetUser(id, user string) error {
	_, err := s.Git.Global("gitswitch-account."+id+".user", user)
	return err
}

// checkKeyNotShared refuses a key already used by another account on the
// same host: the host would always log in as that other account.
func (s *Store) checkKeyNotShared(a Account) error {
	key := filepath.Clean(sys.ExpandHome(a.Key))
	pub, _ := os.ReadFile(key + ".pub")
	for _, o := range s.Load().Accounts {
		if strings.EqualFold(o.ID, a.ID) || o.Key == "" || !gitcli.SameHost(o.Host, a.Host) {
			continue
		}
		ok := filepath.Clean(sys.ExpandHome(o.Key))
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
func (s *Store) DeleteKeyFiles(a *Account) []string {
	if a.Key == "" {
		return nil
	}
	var gone []string
	for _, p := range []string{sys.ExpandHome(a.Key), sys.ExpandHome(a.Key) + ".pub"} {
		if _, err := os.Stat(p); err == nil && s.FS.Remove(p) == nil {
			gone = append(gone, p)
		}
	}
	return gone
}

func (s *Store) SetDefault(id string) error {
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
func (s *Store) AddRule(r Rule) error {
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
		p, err := filepath.Abs(sys.ExpandHome(r.Pattern))
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

func (s *Store) RemoveRule(r Rule) error {
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
	if rp := filepath.ToSlash(sys.RealPath(filepath.FromSlash(p))); rp != p {
		out = append(out, rp)
	}
	return out
}

func includeKey(folder string) string {
	f := strings.TrimSuffix(folder, "/") + "/"
	if sys.CaseInsensitiveFS() {
		return "includeIf.gitdir/i:" + f + ".path"
	}
	return "includeIf.gitdir:" + f + ".path"
}

func (s *Store) AddScanFolder(p string) error {
	abs, err := filepath.Abs(sys.ExpandHome(p))
	if err != nil {
		return err
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
	if !ValidID(newID) {
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
	if a.Key != "" && filepath.Clean(sys.ExpandHome(a.Key)) == filepath.Clean(DefaultKeyPath(oldID)) {
		nk := DefaultKeyPath(newID)
		if _, err := os.Stat(nk); os.IsNotExist(err) && s.FS.Rename(DefaultKeyPath(oldID), nk) == nil {
			_ = s.FS.Rename(DefaultKeyPath(oldID)+".pub", nk+".pub")
			_, _ = s.Git.Global("gitswitch-account."+newID+".key", nk)
		}
	}
	oldFile, newFile := IdentityFile(oldID), IdentityFile(newID)
	if _, err := os.Stat(oldFile); err == nil {
		_ = s.FS.Rename(oldFile, newFile)
	}
	out, _ := s.Git.Run("config", "--global", "--get-regexp", `^includeif\..*\.path$`)
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, " ")
		if ok && filepath.ToSlash(v) == filepath.ToSlash(oldFile) {
			_, _ = s.Git.Global(k, filepath.ToSlash(newFile))
		}
	}
	for _, r := range c.Rules {
		if strings.EqualFold(r.Account, oldID) {
			_ = s.RemoveRule(r)
			r.Account = newID
			_, _ = s.Git.Global("--add", "gitswitch.rule", r.String())
			if r.Kind == "folder" {
				_ = s.writeFolderIncludes(r.Pattern, newID)
			}
		}
	}
	if strings.EqualFold(c.Default, oldID) {
		_, _ = s.Git.Global("gitswitch.default", newID)
	}
	_, err := s.Git.Global("--add", "gitswitch.renamed", oldID+"="+newID)
	return err
}

func (s *Store) writeIdentityFile(a Account) error {
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
func (s *Store) ApplyIdentity(gitDir string, a *Account) {
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
func (s *Store) SetRepoAccount(gitDir string, a *Account) (string, error) {
	if err := s.Git.RepoSet(gitDir, "gitswitch.account", a.ID); err != nil {
		return "", err
	}
	s.ApplyIdentity(gitDir, a)
	if v := s.Git.RepoGet(gitDir, "core.sshCommand"); v != "" && !IsHook(v) {
		return v, s.Git.RepoUnset(gitDir, "core.sshCommand")
	}
	return "", nil
}

// ForgetRepo removes a repo's saved account.
func (s *Store) ForgetRepo(gitDir string) error {
	return s.Git.RepoUnset(gitDir, "gitswitch.account")
}
