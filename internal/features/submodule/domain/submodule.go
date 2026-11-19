// Package domain describes a repo's submodules as the apps show them:
// what .gitmodules says, what `git submodule status` reports, which commit
// the superproject points at and which one is checked out. It also holds
// the checks that keep a submodule update, add or removal from losing work,
// and declares what those use cases need from git (ports). It is pure:
// no git, no files. Import it as submodule.
package domain

import (
	"fmt"
	"path"
	"strings"
)

// States a submodule can be in.
const (
	NotInitialized = "not initialized"
	UpToDate       = "up to date"
	OtherCommit    = "checked out at another commit"
	Conflicts      = "has merge conflicts"
)

// Entry is one submodule.
type Entry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	URL    string `json:"url"`
	Branch string `json:"branch"` // the branch .gitmodules tracks; "" when none is set
	// Recorded is the commit the superproject points at; CheckedOut the one
	// in the submodule's folder ("" when not initialized or conflicted).
	Recorded    string `json:"recorded"`
	CheckedOut  string `json:"checkedOut"`
	State       string `json:"state"`
	Initialized bool   `json:"initialized"`
	// Dirty counts the changed files inside the submodule; -1 when it
	// could not be read in time.
	Dirty int `json:"dirty"`
	// Upstream is the remote branch the recorded commit is compared with
	// ("" when this clone has none); Ahead counts the recorded commit's
	// commits it does not have, Behind the ones it has that the recorded
	// commit lacks.
	Upstream string `json:"upstream"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	// Account is the account the URL's owner maps to (what git uses to
	// fetch it), "" when none is set up or git will ask.
	Account              string `json:"account"`
	AccountReason        string `json:"accountReason"`
	HasRepoConfigAccount bool   `json:"hasRepoConfigAccount"` // the submodule itself saves an account
}

// Line is one line of `git submodule status`.
type Line struct {
	Prefix   byte   // ' ' as recorded, '-' not initialized, '+' another commit, 'U' conflicts
	Hash     string // "" for the all-zero hash of a conflict
	Path     string
	Describe string
}

// ParseStatus reads the output of `git submodule status` (nested
// submodules, from --recursive, have the path of their parent first).
func ParseStatus(out string) []Line {
	var ls []Line
	for _, raw := range strings.Split(out, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if len(raw) < 3 {
			continue
		}
		p := raw[0]
		if p != ' ' && p != '-' && p != '+' && p != 'U' {
			continue
		}
		hash, rest, ok := strings.Cut(raw[1:], " ")
		if !ok || !isHex(hash) {
			continue
		}
		l := Line{Prefix: p, Hash: hash, Path: rest}
		if strings.Trim(hash, "0") == "" {
			l.Hash = ""
		}
		if strings.HasSuffix(rest, ")") {
			if i := strings.LastIndex(rest, " ("); i > 0 {
				l.Path, l.Describe = rest[:i], rest[i+2:len(rest)-1]
			}
		}
		ls = append(ls, l)
	}
	return ls
}

func isHex(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// StateOf names the state a status prefix stands for.
func StateOf(prefix byte) string {
	switch prefix {
	case '-':
		return NotInitialized
	case '+':
		return OtherCommit
	case 'U':
		return Conflicts
	}
	return UpToDate
}

// Module is one section of .gitmodules.
type Module struct {
	Name, Path, URL, Branch string
}

// ParseModules reads `git config -f .gitmodules -z --get-regexp ^submodule\.`
// (entries "key\nvalue" ended by NUL), in the order the file lists them.
// A section without a path is dropped.
func ParseModules(out string) []Module {
	var ms []Module
	at := map[string]int{}
	for _, rec := range strings.Split(out, "\x00") {
		key, val, _ := strings.Cut(rec, "\n")
		if !strings.HasPrefix(key, "submodule.") {
			continue
		}
		key = strings.TrimPrefix(key, "submodule.")
		i := strings.LastIndex(key, ".")
		if i <= 0 {
			continue
		}
		name, variable := key[:i], strings.ToLower(key[i+1:])
		n, ok := at[name]
		if !ok {
			n = len(ms)
			at[name] = n
			ms = append(ms, Module{Name: name})
		}
		switch variable {
		case "path":
			ms[n].Path = val
		case "url":
			ms[n].URL = val
		case "branch":
			ms[n].Branch = val
		}
	}
	out2 := ms[:0]
	for _, m := range ms {
		if m.Path != "" {
			out2 = append(out2, m)
		}
	}
	return out2
}

// Build joins .gitmodules with the two status listings: plain (what is
// checked out) and cached (what the superproject records).
func Build(mods []Module, plain, cached []Line) []Entry {
	byPlain, byCached := map[string]Line{}, map[string]Line{}
	for _, l := range plain {
		byPlain[l.Path] = l
	}
	for _, l := range cached {
		byCached[l.Path] = l
	}
	es := make([]Entry, 0, len(mods))
	for _, m := range mods {
		e := Entry{Name: m.Name, Path: m.Path, URL: m.URL, Branch: m.Branch, State: NotInitialized, Dirty: -1}
		if l, ok := byPlain[m.Path]; ok {
			e.State = StateOf(l.Prefix)
			e.Initialized = l.Prefix != '-'
			e.Recorded, e.CheckedOut = l.Hash, l.Hash
			if l.Prefix == '-' || l.Prefix == 'U' {
				e.CheckedOut = ""
			}
		}
		if l, ok := byCached[m.Path]; ok && l.Hash != "" {
			e.Recorded = l.Hash
		}
		if !e.Initialized {
			e.Dirty = 0
		}
		es = append(es, e)
	}
	return es
}

// ResolveURL turns a relative submodule URL (./x or ../x, relative to the
// superproject's own remote) into a full one; other URLs come back as given.
func ResolveURL(base, rel string) string {
	rel = strings.TrimSpace(rel)
	if !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "../") {
		return rel
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return rel
	}
	var head, tail string
	if i := strings.Index(base, "://"); i >= 0 {
		j := strings.Index(base[i+3:], "/")
		if j < 0 {
			return rel
		}
		head, tail = base[:i+3+j+1], base[i+3+j+1:]
	} else if i := strings.Index(base, ":"); i > 0 && !strings.Contains(base[:i], "/") {
		head, tail = base[:i+1], base[i+1:]
	} else {
		return rel // a local path: nothing to resolve against
	}
	return head + path.Join(tail, rel)
}

// CheckPath cleans a path inside the repo: forward slashes, relative, not
// above the repo and not touching .git.
func CheckPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	bad := fmt.Errorf("not a folder inside the repo: %q", p)
	if p == "" || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\n\r") {
		return "", bad
	}
	if len(p) >= 2 && p[1] == ':' {
		return "", bad
	}
	p = path.Clean(p)
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." || strings.EqualFold(part, ".git") {
			return "", bad
		}
	}
	return p, nil
}

// CheckBranch refuses a branch name git would not accept or that could be
// read as an option.
func CheckBranch(b string) error {
	if b == "" {
		return nil
	}
	if strings.HasPrefix(b, "-") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock") ||
		strings.Contains(b, "..") || strings.ContainsAny(b, " \t\n\r~^:?*[\\\x00") || strings.HasPrefix(b, "/") {
		return fmt.Errorf("not a branch name: %q", b)
	}
	return nil
}

// CheckModuleName refuses a .gitmodules name that would point outside
// .git/modules (the folder removing a submodule cleans).
func CheckModuleName(n string) error {
	if n == "" || strings.HasPrefix(n, "/") || strings.ContainsAny(n, "\\\x00") {
		return fmt.Errorf("unsafe submodule name %q", n)
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("unsafe submodule name %q", n)
		}
	}
	return nil
}

// Find returns the entry at a path.
func Find(es []Entry, p string) (Entry, bool) {
	for _, e := range es {
		if e.Path == p {
			return e, true
		}
	}
	return Entry{}, false
}

// Targets picks the entries a command works on: the one at path, or all
// when path is "".
func Targets(es []Entry, p string) ([]Entry, error) {
	if p == "" {
		return es, nil
	}
	e, ok := Find(es, p)
	if !ok {
		return nil, fmt.Errorf("%s is not a submodule of this repo (gitswitch submodule list shows them)", p)
	}
	return []Entry{e}, nil
}

// CheckUpdate says why an update can't go ahead, or "". Moving a
// submodule to another commit would overwrite changes inside it; with
// remote every initialized one moves. Merge conflicts always stop it.
func CheckUpdate(targets []Entry, remote bool) string {
	var parts []string
	for _, e := range targets {
		switch {
		case !e.Initialized:
		case e.State == Conflicts:
			parts = append(parts, fmt.Sprintf("%s has merge conflicts; settle them in the superproject first", e.Path))
		case e.Dirty > 0 && (remote || e.CheckedOut != e.Recorded):
			parts = append(parts, fmt.Sprintf("%s has %d changed file(s) inside it that the update would overwrite; commit, stash or discard them there first", e.Path, e.Dirty))
		}
	}
	return strings.Join(parts, "; ")
}

// CheckRemove says why a submodule can't be removed now, or "". Changes
// and commits that exist only in its folder would be lost.
func CheckRemove(e Entry, unpushed int, force bool) string {
	if force {
		return ""
	}
	var what []string
	if e.Dirty > 0 {
		what = append(what, fmt.Sprintf("%d changed file(s)", e.Dirty))
	}
	if unpushed > 0 {
		what = append(what, fmt.Sprintf("%d commit(s) that no remote has", unpushed))
	}
	if len(what) == 0 {
		return ""
	}
	return fmt.Sprintf("%s has %s inside it that removing it would delete; commit and push them, or remove it anyway with --force", e.Path, strings.Join(what, " and "))
}

// RemovePlan lists, in words, what removing a submodule deletes.
func RemovePlan(e Entry, unpushed int) []string {
	ps := []string{
		fmt.Sprintf("the folder %s (its checkout, with whatever is in it)", e.Path),
		"its entry in .gitmodules and in the repo's settings (staged; commit to keep the removal)",
	}
	if e.Initialized {
		ps = append(ps, fmt.Sprintf("the downloaded copy of %s under .git/modules/%s", e.Name, e.Name))
	}
	if e.Dirty > 0 {
		ps = append(ps, fmt.Sprintf("%d changed file(s) inside it", e.Dirty))
	}
	if unpushed > 0 {
		ps = append(ps, fmt.Sprintf("%d commit(s) that no remote has", unpushed))
	}
	return ps
}

// AccountNote says in words which account a new submodule's URL would use
// and warns when it is not the repo's own.
func AccountNote(url, repoAccount, subAccount string) string {
	switch {
	case subAccount == "":
		return fmt.Sprintf("No account is picked for %s by your rules, so git will ask which to use", url)
	case repoAccount != "" && subAccount != repoAccount:
		return fmt.Sprintf("%s uses the account %s (from your rules), not this repo's %s: fetching it and commits made inside it are %s's", url, subAccount, repoAccount, subAccount)
	}
	return ""
}

// Change is a submodule an update moved.
type Change struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Updated is the result of an update, init or sync.
type Updated struct {
	Changed []Change `json:"changed"`
	Message string   `json:"message"`
	// RecordedChanged is true after an update with remote: the superproject
	// now has changes to commit, as it points at the branches' tips.
	RecordedChanged bool `json:"recordedChanged"`
}

// Added is the result of adding a submodule.
type Added struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Branch  string `json:"branch"`
	Account string `json:"account"`
	Note    string `json:"note"`
	Message string `json:"message"`
}

// Removed is the result of removing a submodule.
type Removed struct {
	Path    string   `json:"path"`
	Deleted []string `json:"deleted"`
	Message string   `json:"message"`
}

// RemoveInfo is what removing a submodule would delete, shown before asking.
type RemoveInfo struct {
	Path    string   `json:"path"`
	Deleted []string `json:"deleted"`
	Blocked string   `json:"blocked"`
}

// Git is what the submodule use cases need from git. Worktree is the
// superproject's working folder; path is the submodule's path in it.
type Git interface {
	// Modules is the raw `git config -f .gitmodules -z --get-regexp`
	// output ("" when the repo has no .gitmodules).
	Modules(worktree string) (string, error)
	// Status is `git submodule status`, or with cached the commits the
	// superproject records.
	Status(worktree string, cached bool) (string, error)
	// Origin is the superproject's origin URL ("" when none).
	Origin(worktree string) string
	// Dirty counts the changed files inside a submodule, -1 when git did
	// not answer in time.
	Dirty(worktree, path string) int
	// Upstream compares the recorded commit with the tracked branch (or
	// the remote's default branch): its name and the two counts; "" when
	// this clone has no such branch.
	Upstream(worktree, path, recorded, branch string) (upstream string, ahead, behind int)
	// Unpushed counts commits inside a submodule that no remote branch has.
	Unpushed(worktree, path string) int
	// GitDir is an initialized submodule's own git dir ("" when none).
	GitDir(worktree, path string) string
	// PathExists reports whether anything is at path in the working folder
	// or the index.
	PathExists(worktree, path string) bool
	// Update runs `git submodule update --init` for the paths (all when
	// none), with --recursive and --remote as asked.
	Update(worktree string, paths []string, remote, recursive bool) error
	Sync(worktree string) error
	Add(worktree, url, path, branch string) error
	// Deinit empties the submodule's folder and its settings entry.
	Deinit(worktree, path string) error
	// RemovePath is `git rm -f`: the folder, the .gitmodules entry.
	RemovePath(worktree, path string) error
	// DropModule deletes the downloaded copy under .git/modules.
	DropModule(worktree, name string) error
}
