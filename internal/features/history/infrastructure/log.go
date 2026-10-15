// Package infrastructure reads the commit history with git and rewrites
// commit authors. Import it as historyinfra.
package infrastructure

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Log reads commits with git. It implements the history domain's Log.
type Log struct {
	Git *gitinfra.Runner
}

// allRefs is every branch, remote branch and tag, but not the stash (its
// commits are saved uncommitted changes, not history) or GitSwitch's own
// refs (refs/gitswitch/undo keeps what an undo goes back to). --exclude
// only applies to the --all after it.
var allRefs = [3]string{"--exclude=refs/stash", "--exclude=refs/gitswitch/*", "--all"}

func (l *Log) Commits(gitDir string, limit int, f history.Filter) ([]history.Commit, error) {
	git := l.Git
	order := "--topo-order"
	if f.Order == "date" {
		order = "--date-order"
	}
	args := []string{"--git-dir=" + gitDir, "log", order, "-n", strconv.Itoa(limit), "--decorate=full",
		"--format=%H%x1f%P%x1f%ae%x1f%an%x1f%at%x1f%D%x1f%s%x1e"}
	args = append(args, filterArgs(f)...)
	// The revisions after every option, so a name can never be one.
	if f.Ref != "" {
		args = append(args, "--end-of-options", f.Ref)
	} else {
		args = append(args, allRefs[:]...)
	}
	if f.Path != "" {
		args = append(args, "--", f.Path)
	}
	out, err := git.Run(args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		if f.Ref != "" && (strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "bad revision")) {
			return nil, fmt.Errorf("there is no branch or tag %s in this repo (any more)", f.Ref)
		}
		return nil, err
	}
	var cs []history.Commit
	firstParent := f.FirstParent
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		t, _ := strconv.ParseInt(f[4], 10, 64)
		c := history.Commit{Hash: f[0], Email: f[2], Author: f[3], Time: t, Labels: history.ParseLabels(f[5]), Subject: f[6]}
		c.Refs = history.RefsText(c.Labels)
		if len(c.Hash) >= 7 {
			c.Short = c.Hash[:7]
		}
		if f[1] != "" {
			c.Parents = strings.Fields(f[1])
		}
		c.Merge = len(c.Parents) > 1
		if firstParent && c.Merge {
			// Only the first parent is listed: the graph must not draw
			// lines to the others, which are not there.
			c.Parents = c.Parents[:1]
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// filterArgs turns a filter into git log options. Values are passed as
// --opt=value, so they can never be read as other options.
func filterArgs(f history.Filter) []string {
	var args []string
	if f.Author != "" || f.Text != "" {
		args = append(args, "--regexp-ignore-case", "--fixed-strings")
	}
	if f.Author != "" {
		args = append(args, "--author="+f.Author)
	}
	if f.Text != "" {
		args = append(args, "--grep="+f.Text)
	}
	if f.Since != "" {
		args = append(args, "--since="+f.Since)
	}
	if f.FirstParent {
		args = append(args, "--first-parent")
	}
	if f.NoMerges {
		args = append(args, "--no-merges")
	}
	if f.Until != "" {
		args = append(args, "--until="+f.Until)
	}
	return args
}

func (l *Log) Unpushed(gitDir string) map[string]bool {
	git := l.Git
	set := map[string]bool{}
	out, err := git.Run("--git-dir="+gitDir, "rev-list", "--branches", "--not", "--remotes")
	if err != nil {
		return set
	}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

func (l *Log) OnRemote(gitDir, hash string) bool {
	out, err := l.Git.Run("--git-dir="+gitDir, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", hash, "refs/remotes")
	return err == nil && out != ""
}

func (l *Log) Graph(gitDir string, n int, color bool) (string, error) {
	gargs := []string{"--git-dir=" + gitDir, "log", "--graph", allRefs[0], allRefs[1], allRefs[2], "-n", strconv.Itoa(n), "--format=%x1f%H"}
	if color {
		gargs = append(gargs, "--color=always")
	}
	out, err := l.Git.Run(gargs...)
	if err != nil && strings.Contains(err.Error(), "does not have any commits") {
		return "", history.ErrNoCommits
	}
	return out, err
}

func (l *Log) LocalCommits(wt string) ([]string, error) {
	if err := l.upstreamKnown(wt); err != nil {
		return nil, err
	}
	out, err := l.Git.Run("-C", wt, "rev-list", "--reverse", "HEAD", "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// upstreamKnown refuses to judge what is pushed when the branch is set to
// push to a remote branch git has no local copy of: a single-branch or
// shallow clone after `git push -u`, a narrowed fetch refspec, or a remote
// branch deleted after merging. Its commits may be on the server even
// though no remote-tracking ref contains them.
func (l *Log) upstreamKnown(wt string) error {
	b := l.Branch(wt)
	if b == "" {
		return nil
	}
	r, _ := l.Git.Run("-C", wt, "config", "--get", "branch."+b+".remote")
	if r == "" || r == "." {
		return nil
	}
	if _, err := l.Git.Run("-C", wt, "rev-parse", "--verify", "-q", b+"@{upstream}"); err != nil {
		return fmt.Errorf("%s pushes to %s, but this clone has no copy of that remote branch (it may be a single-branch clone, or the branch was deleted on the server), so gitswitch can't tell which commits are pushed; run `git fetch %s` and try again, or revert instead", b, r, r)
	}
	return nil
}

func (l *Log) AuthorEmail(wt, hash string) string {
	e, _ := l.Git.Run("-C", wt, "log", "-1", "--format=%ae", hash)
	return e
}

func (l *Log) HeadAuthorName(wt string) string {
	name, _ := l.Git.Run("-C", wt, "log", "-1", "--format=%an", "HEAD")
	return name
}

func (l *Log) Rewritten(wt, first string) ([]string, error) {
	args := []string{"-C", wt, "rev-list", "HEAD"}
	if p, err := l.Git.Run("-C", wt, "rev-parse", "--verify", "-q", first+"^"); err == nil && p != "" {
		args = append(args, "^"+p)
	}
	out, err := l.Git.Run(append(args, "--")...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (l *Log) RewriteAuthor(wt, name, email, first string, wrong []string) error {
	git := l.Git
	if len(wrong) == 0 {
		return nil
	}
	safe := strings.NewReplacer(`"`, "", "`", "", "$", "", `\`, "").Replace
	author := safe(name) + " <" + safe(email) + ">"
	// Only commits by one of the wrong emails are amended. Any other commit
	// the rebase replays (a teammate's, say) keeps its author.
	pats := make([]string, len(wrong))
	for i, e := range wrong {
		pats[i] = `"` + safe(e) + `"`
	}
	execLine := `case "$(git log -1 --format=%ae)" in ` + strings.Join(pats, "|") + `) git commit --amend --allow-empty --no-edit --no-verify --author="` + author + `";; esac`
	args := []string{"-C", wt, "-c", "user.email=" + email, "-c", "user.name=" + name,
		"rebase", "--rebase-merges", "--keep-empty", "--autostash", "--exec", execLine}
	if p, err := git.Run("-C", wt, "rev-parse", "--verify", "-q", first+"^"); err == nil && p != "" {
		args = append(args, p)
	} else {
		args = append(args, "--root")
	}
	stash := l.stashTop(wt)
	cmd := exec.Command(gitinfra.Program(), args...)
	cmd.Env = append(os.Environ(), "GIT_SEQUENCE_EDITOR=:", "GIT_EDITOR=:")
	process.HideWindow(cmd)
	if b, err := cmd.CombinedOutput(); err != nil {
		_, _ = git.Run("-C", wt, "rebase", "--abort")
		return fmt.Errorf("rewrite failed, nothing changed: %s", strings.TrimSpace(string(b)))
	}
	return l.autostashKept(wt, stash, "fixed the author")
}

func (l *Log) Resolve(wt, rev string) (history.Target, error) {
	out, err := l.Git.Run("-C", wt, "log", "-1", "--format=%H%x1f%P%x1f%s", rev+"^{commit}", "--")
	if err != nil {
		return history.Target{}, fmt.Errorf("no commit %q in this repo", rev)
	}
	f := strings.SplitN(out, "\x1f", 3)
	if len(f) < 3 {
		return history.Target{}, fmt.Errorf("no commit %q in this repo", rev)
	}
	t := history.Target{Hash: f[0], Subject: f[2], Parents: strings.Fields(f[1])}
	if len(t.Hash) >= 7 {
		t.Short = t.Hash[:7]
	}
	return t, nil
}

func (l *Log) Show(gitDir, rev string) (history.Detail, error) {
	// --diff-merges=first-parent: a merge lists what it brought into the
	// branch. The record separator ends the message, numstat lines follow.
	out, err := l.Git.Run("--git-dir="+gitDir, "log", "-1", "--no-color", "--numstat", "--diff-merges=first-parent",
		"--format=%H%x1f%B%x1e", rev+"^{commit}", "--")
	if err != nil {
		return history.Detail{}, fmt.Errorf("no commit %q in this repo", rev)
	}
	head, stat, _ := strings.Cut(out, "\x1e")
	hash, msg, ok := strings.Cut(head, "\x1f")
	if !ok {
		return history.Detail{}, fmt.Errorf("no commit %q in this repo", rev)
	}
	d := history.Detail{Hash: strings.TrimSpace(hash), Message: strings.TrimSpace(msg)}
	history.ParseNumstat(&d, stat)
	return d, nil
}

func (l *Log) FileDiff(gitDir, rev, path string) (history.Diff, error) {
	out, err := l.Git.Run("--git-dir="+gitDir, "show", "--no-color", "--format=", "--diff-merges=first-parent", "--no-ext-diff",
		"--end-of-options", rev+"^{commit}", "--", path)
	if err != nil {
		return history.Diff{}, fmt.Errorf("no commit %q in this repo", rev)
	}
	d := history.Diff{Path: path, Text: out}
	if len(d.Text) > history.MaxDiffBytes {
		d.Text, d.Truncated = d.Text[:history.MaxDiffBytes], true
	}
	return d, nil
}

func (l *Log) NotPicked(gitDir, ref string) map[string]bool {
	set := map[string]bool{}
	// "+": no equivalent on HEAD; "=": cherry-picked already.
	out, err := l.Git.Run("--git-dir="+gitDir, "rev-list", "--cherry-mark", "--right-only", "--no-merges", "--end-of-options", "HEAD..."+ref)
	if err != nil {
		return set
	}
	for _, line := range strings.Fields(out) {
		if h, ok := strings.CutPrefix(line, "+"); ok {
			set[h] = true
		}
	}
	return set
}

func (l *Log) OnHead(gitDir string, since int64) map[string]bool {
	set := map[string]bool{}
	out, err := l.Git.Run("--git-dir="+gitDir, "rev-list", "--since="+strconv.FormatInt(since, 10), "HEAD", "--")
	if err != nil {
		return set
	}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set
}

func (l *Log) Branch(wt string) string {
	b, _ := l.Git.Run("-C", wt, "symbolic-ref", "-q", "--short", "HEAD")
	return b
}

func (l *Log) InProgress(gitDir string) string { return gitinfra.InProgress(gitDir) }

func (l *Log) OnBranch(wt, hash string) bool {
	_, err := l.Git.Run("-C", wt, "merge-base", "--is-ancestor", hash, "HEAD")
	return err == nil
}

func (l *Log) After(wt, hash string) ([]string, error) {
	out, err := l.Git.Run("-C", wt, "rev-list", hash+"..HEAD")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (l *Log) Revert(wt string, t history.Target) error {
	args := []string{"revert", "--no-edit"}
	if t.IsMerge() {
		args = append(args, "-m", "1") // undo what the merge brought in
	}
	if err := l.Git.Change(wt, append(args, t.Hash)...); err != nil {
		if gitinfra.InProgress(l.Git.GitDirFor(wt)) == "revert" {
			_ = l.Git.Change(wt, "revert", "--abort")
		}
		return fmt.Errorf("revert failed, nothing changed: %v", err)
	}
	return nil
}

func (l *Log) Drop(wt string, t history.Target) error {
	stash := l.stashTop(wt)
	err := l.Git.Change(wt, "rebase", "--rebase-merges", "--autostash", "--onto", t.Hash+"^", t.Hash)
	if err != nil {
		if gitinfra.InProgress(l.Git.GitDirFor(wt)) == "rebase" {
			_ = l.Git.Change(wt, "rebase", "--abort")
		}
		return fmt.Errorf("drop failed, nothing changed: %v", err)
	}
	return l.autostashKept(wt, stash, "dropped "+t.Short)
}

// stashTop is the newest stash entry, "" when there is none.
func (l *Log) stashTop(wt string) string {
	h, _ := l.Git.Run("-C", wt, "rev-parse", "-q", "--verify", "refs/stash")
	return h
}

// autostashKept explains what happened when a rebase with --autostash
// could not put the uncommitted changes back cleanly. Git still reports
// success then: it leaves conflict markers in the files and keeps the
// changes as a new stash entry. done says what the rebase did.
func (l *Log) autostashKept(wt, before, done string) error {
	if after := l.stashTop(wt); after == "" || after == before {
		return nil
	}
	return stashKept(fmt.Sprintf("%s, but your uncommitted changes clash with the new history. They are safe in the stash (the newest entry in `git stash list`, named autostash), and the files that clash now have conflict markers. Either fix those files and run `git stash drop`, or discard the half-applied changes with `git reset --hard` and get them back later with `git stash pop`", done))
}

// stashKept is an explained history.ErrStashKept.
type stashKept string

func (e stashKept) Error() string        { return string(e) }
func (e stashKept) Is(target error) bool { return target == history.ErrStashKept }

func (l *Log) Reset(wt, hash string) error {
	return l.Git.Change(wt, "reset", "--mixed", "-q", hash)
}

func (l *Log) Order(wt string, hashes []string) ([]string, error) {
	want := map[string]bool{}
	for _, h := range hashes {
		want[h] = true
	}
	out, err := l.Git.Run(append(append([]string{"-C", wt, "rev-list", "--topo-order", "--reverse"}, hashes...), "^HEAD", "--")...)
	if err != nil {
		return nil, err
	}
	var sorted []string
	for _, h := range strings.Fields(out) {
		if want[h] {
			sorted = append(sorted, h)
		}
	}
	return sorted, nil
}

func (l *Log) CherryPick(wt string, ts []history.Target) error {
	args := []string{"cherry-pick"}
	for _, t := range ts {
		args = append(args, t.Hash)
	}
	if err := l.Git.Change(wt, args...); err != nil {
		if gitinfra.InProgress(l.Git.GitDirFor(wt)) == "cherry-pick" {
			_ = l.Git.Change(wt, "cherry-pick", "--abort")
		}
		return fmt.Errorf("cherry-pick failed, nothing changed: %v", err)
	}
	return nil
}
