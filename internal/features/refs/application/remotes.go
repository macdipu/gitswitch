package application

import (
	"errors"
	"fmt"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// remoteGit is the extra port of the remote features, looked for on the
// Refs the service was given.
func (s *Service) remoteGit() (refs.RemoteGit, error) {
	g, ok := s.Refs.(refs.RemoteGit)
	if !ok {
		return nil, errors.New("remotes are not available")
	}
	return g, nil
}

// accountFor is the gitswitch account that talking to url from this repo
// would use ("" when none is set up for its host), and the owner rule's
// account when it differs from the repo's saved one.
func (s *Service) accountFor(gitDir, url string) (used, ownerRule string) {
	if s.Accounts == nil || s.Repos == nil {
		return "", ""
	}
	cfg := s.Accounts.Load()
	info := s.Repos.InfoFor(cfg, gitDir)
	host, path := remote.Parse(url)
	cands := resolve.Candidates(cfg, host)
	if d := resolve.Decide(cfg, cands, resolve.Input{SavedID: info.Saved, RepoPath: path, Worktree: func() string { return s.ruleFolder(gitDir) }}); d != nil {
		used = d.Account.ID
	}
	if info.Saved != "" && !resolve.AsksEveryTime(info.Saved) {
		if d := resolve.Decide(cfg, cands, resolve.Input{RepoPath: path}); d != nil && d.Reason == resolve.OwnerRule && d.Account.ID != used {
			ownerRule = d.Account.ID
		}
	}
	return used, ownerRule
}

// accountNote says in words which account a URL uses, and when the owner
// rule would pick another.
func (s *Service) accountNote(gitDir, url string) (acct, note string) {
	acct, rule := s.accountFor(gitDir, url)
	host, path := remote.Parse(url)
	owner, _, _ := strings.Cut(path, "/")
	switch {
	case rule != "" && acct != "":
		note = fmt.Sprintf("%s belongs to %s, which your rules give to the account %s, but this repo is saved as %s, so it fetches and pushes as %s. To use %s here: gitswitch use %s", url, owner, rule, acct, acct, rule, rule)
	case rule != "":
		note = fmt.Sprintf("%s belongs to %s, which your rules give to the account %s, but this repo's saved account has none for %s, so git will ask which to use", url, owner, rule, remote.CanonicalHost(host))
	case acct == "" && s.Accounts != nil && host != "":
		note = fmt.Sprintf("No account is set up for %s, so git will ask which to use (gitswitch account add)", remote.CanonicalHost(host))
	}
	return acct, note
}

// Remotes lists the repo's remotes with the account each URL would use.
func (s *Service) Remotes(gitDir string) ([]refs.Remote, error) {
	g, err := s.remoteGit()
	if err != nil {
		return nil, err
	}
	confs, err := g.Remotes(gitDir)
	if err != nil {
		return nil, err
	}
	h, _ := s.Refs.Head(gitDir)
	cur := ""
	if h.Branch != "" {
		cur, _ = s.Refs.PushTarget(gitDir, h.Branch)
	}
	out := []refs.Remote{}
	for _, c := range confs {
		r := refs.Remote{Name: c.Name, FetchURL: c.FetchURL, PushURL: c.PushURL, Upstream: cur == c.Name, Tracking: g.TrackingBranches(gitDir, c.Name)}
		if r.Tracking == nil {
			r.Tracking = []string{}
		}
		host, _ := remote.Parse(c.PushURL)
		r.Host = remote.CanonicalHost(host)
		r.Account, _ = s.accountFor(gitDir, c.PushURL)
		out = append(out, r)
	}
	return out, nil
}

func (s *Service) findRemote(gitDir, name string) (refs.RemoteGit, refs.RemoteConf, error) {
	g, err := s.remoteGit()
	if err != nil {
		return nil, refs.RemoteConf{}, err
	}
	confs, err := g.Remotes(gitDir)
	if err != nil {
		return nil, refs.RemoteConf{}, err
	}
	for _, c := range confs {
		if c.Name == name {
			return g, c, nil
		}
	}
	names := make([]string, len(confs))
	for i, c := range confs {
		names[i] = c.Name
	}
	have := "this repo has no remote"
	if len(names) > 0 {
		have = "its remotes are " + strings.Join(names, ", ")
	}
	return nil, refs.RemoteConf{}, fmt.Errorf("no remote called %q: %s", name, have)
}

// AddRemote adds a remote. The URL must be one GitSwitch works with (SSH or
// HTTPS); a name already used is refused. The result says which account the
// URL would use, and when its owner maps to another account than the one
// saved for the repo. It is not undoable (see the Undo feature: it only
// moves HEAD), but removing the remote again is one command.
func (s *Service) AddRemote(gitDir, name, url string) (refs.RemoteChanged, error) {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	c := refs.RemoteChanged{Name: name, URL: url, Branches: []string{}}
	g, err := s.remoteGit()
	if err != nil {
		return c, err
	}
	if err := refs.CheckRemoteName(name); err != nil {
		return c, err
	}
	if err := remote.CheckCloneURL(url); err != nil {
		return c, err
	}
	have, err := g.Remotes(gitDir)
	if err != nil {
		return c, err
	}
	for _, r := range have {
		if r.Name == name {
			return c, fmt.Errorf("there is already a remote called %s (%s); choose another name, or change its URL: gitswitch remote set-url %s <url>", name, r.FetchURL, name)
		}
	}
	host, _ := remote.Parse(url)
	c.Host = remote.CanonicalHost(host)
	c.Account, c.Note = s.accountNote(gitDir, url)
	if s.FS.Dry(fmt.Sprintf("add the remote %s at %s (git remote add)", name, url)) {
		return c, nil
	}
	return c, g.AddRemote(gitDir, name, url)
}

// RemoveRemote removes a remote from this clone: its remote-tracking
// branches go, and the branches that pulled from it lose their upstream.
// That is refused unless force is set when any branch tracks it. The server
// is not touched. Not undoable; the result carries the URL to add it back.
func (s *Service) RemoveRemote(gitDir, name string, force bool) (refs.RemoteChanged, error) {
	name = strings.TrimSpace(name)
	c := refs.RemoteChanged{Name: name, Branches: []string{}}
	g, conf, err := s.findRemote(gitDir, name)
	if err != nil {
		return c, err
	}
	c.Was = conf.FetchURL
	if t := g.TrackingBranches(gitDir, name); len(t) > 0 {
		c.Branches = t
		if !force {
			return c, fmt.Errorf("%s pull%s from %s, so removing it would leave %s without an upstream (no update, no push until you set one). Remove it anyway only with force", strings.Join(t, ", "), map[bool]string{true: "s"}[len(t) == 1], name, map[bool]string{true: "it", false: "them"}[len(t) == 1])
		}
	}
	if s.FS.Dry(fmt.Sprintf("remove the remote %s (%s) and its remote-tracking branches from this clone (git remote remove)", name, conf.FetchURL)) {
		return c, nil
	}
	if err := g.RemoveRemote(gitDir, name); err != nil {
		return c, err
	}
	c.Note = "The server is untouched. To add it back: gitswitch remote add " + name + " " + conf.FetchURL
	return c, nil
}

// RenameRemote renames a remote; the branches that track it keep tracking
// it under the new name.
func (s *Service) RenameRemote(gitDir, from, to string) (refs.RemoteChanged, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	c := refs.RemoteChanged{Name: from, NewName: to, Branches: []string{}}
	g, conf, err := s.findRemote(gitDir, from)
	if err != nil {
		return c, err
	}
	if err := refs.CheckRemoteName(to); err != nil {
		return c, err
	}
	if to == from {
		return c, errors.New("the new name is the same as the old one")
	}
	all, _ := g.Remotes(gitDir)
	for _, r := range all {
		if r.Name == to {
			return c, fmt.Errorf("there is already a remote called %s (%s); choose another name", to, r.FetchURL)
		}
	}
	c.URL = conf.FetchURL
	if t := g.TrackingBranches(gitDir, from); t != nil {
		c.Branches = t
	}
	if s.FS.Dry(fmt.Sprintf("rename the remote %s to %s (git remote rename); its remote-tracking branches and the branches that track it follow", from, to)) {
		return c, nil
	}
	if err := g.RenameRemote(gitDir, from, to); err != nil {
		return c, err
	}
	if len(c.Branches) > 0 {
		c.Note = strings.Join(c.Branches, ", ") + " now track" + map[bool]string{true: "s"}[len(c.Branches) == 1] + " " + to + "."
	}
	return c, nil
}

// SetRemoteURL points a remote at another URL. The URL is checked as on
// clone, and the result says which account it would use. A remote with its
// own push URL keeps it (the note says so).
func (s *Service) SetRemoteURL(gitDir, name, url string) (refs.RemoteChanged, error) {
	name, url = strings.TrimSpace(name), strings.TrimSpace(url)
	c := refs.RemoteChanged{Name: name, URL: url, Branches: []string{}}
	g, conf, err := s.findRemote(gitDir, name)
	if err != nil {
		return c, err
	}
	if err := remote.CheckCloneURL(url); err != nil {
		return c, err
	}
	c.Was = conf.FetchURL
	if url == conf.FetchURL {
		return c, fmt.Errorf("%s already points at %s", name, url)
	}
	host, _ := remote.Parse(url)
	c.Host = remote.CanonicalHost(host)
	c.Account, c.Note = s.accountNote(gitDir, url)
	if conf.PushURL != conf.FetchURL {
		extra := "It has its own push URL (" + conf.PushURL + "), which is unchanged."
		if c.Note != "" {
			c.Note += ". " + extra
		} else {
			c.Note = extra
		}
	}
	if s.FS.Dry(fmt.Sprintf("point the remote %s at %s (was %s; git remote set-url)", name, url, conf.FetchURL)) {
		return c, nil
	}
	return c, g.SetRemoteURL(gitDir, name, url)
}

// CanDeleteRemoteBranch runs the checks of DeleteRemoteBranch without
// deleting anything, so a question can say what is at stake.
func (s *Service) CanDeleteRemoteBranch(gitDir, ref string, force bool) (refs.RemoteBranchDeleted, error) {
	d, _, err := s.planRemoteDelete(gitDir, ref, force)
	return d, err
}

// planRemoteDelete checks a remote branch can be deleted and describes it.
func (s *Service) planRemoteDelete(gitDir, ref string, force bool) (refs.RemoteBranchDeleted, refs.RemoteGit, error) {
	ref = strings.TrimSpace(ref)
	d := refs.RemoteBranchDeleted{Tracking: []string{}}
	g, err := s.remoteGit()
	if err != nil {
		return d, nil, err
	}
	rem, branch, ok := refs.SplitRemoteBranch(ref, s.Refs.RemoteNames(gitDir))
	if !ok || strings.HasPrefix(ref, "-") {
		return d, g, fmt.Errorf("%q is not a remote branch: name it with its remote, like origin/feature/x", ref)
	}
	d.Remote, d.Branch = rem, branch
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return d, g, err
	}
	b := refs.Find(all, ref)
	if b == nil || !b.Remote {
		return d, g, fmt.Errorf("this clone has no remote branch %s (fetch first: gitswitch fetch)", ref)
	}
	d.Hash = b.Hash
	def := s.Refs.DefaultBranch(gitDir, rem)
	if branch == def || (def == "" && (branch == "main" || branch == "master")) {
		if !force {
			return d, g, fmt.Errorf("%s is the default branch of %s: deleting it would break clones and pull requests. Delete it anyway only with force", ref, rem)
		}
	}
	if h, err := s.Refs.Head(gitDir); err == nil && h.Branch != "" && h.Upstream == ref && !force {
		return d, g, fmt.Errorf("you are on %s, which pulls from %s: deleting it would leave %s without an upstream. Switch to another branch first, or delete it anyway only with force", h.Branch, ref, h.Branch)
	}
	for _, l := range all {
		if !l.Remote && l.Upstream == ref {
			d.Tracking = append(d.Tracking, l.Name)
		}
	}
	if s.Repos != nil && s.Accounts != nil {
		d.Account = s.Repos.InfoFor(s.Accounts.Load(), gitDir).Account
	}
	d.Restore = fmt.Sprintf("git push %s %s:refs/heads/%s", rem, b.Hash, branch)
	return d, g, nil
}

// DeleteRemoteBranch deletes a branch on the server (git push <remote>
// --delete), through the repo's account. ref is the remote branch as listed,
// e.g. origin/feature/x. It refuses the remote's default branch, and the
// branch the current branch pulls from, unless force is set. The result
// tells how to push it back (Restore): the server may still refuse if it
// already cleaned up, and other clones keep the commits meanwhile.
func (s *Service) DeleteRemoteBranch(gitDir, ref string, force bool, progress func(string)) (refs.RemoteBranchDeleted, error) {
	d, g, err := s.planRemoteDelete(gitDir, ref, force)
	if err != nil {
		return d, err
	}
	rem, branch := d.Remote, d.Branch
	as := ""
	if d.Account != "" {
		as = " as " + d.Account
	}
	if s.FS.Dry(fmt.Sprintf("delete the branch %s on %s%s (git push %s --delete refs/heads/%s)", branch, rem, as, rem, branch)) {
		return d, nil
	}
	if err := g.DeleteRemoteBranch(s.Locator.WorktreeOf(gitDir), rem, branch, progress); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "remote ref does not exist") {
			return d, fmt.Errorf("%v\n\n%s is not on %s any more (someone deleted it already). Forget the local copy with: gitswitch fetch --prune", err, branch, rem)
		}
		if strings.Contains(low, "protected") || strings.Contains(low, "refusing to delete the current branch") || strings.Contains(low, "deletion of the current branch") {
			return d, fmt.Errorf("%v\n\nthe server refuses to delete %s (it is protected, or the server's default branch)", err, branch)
		}
		return d, err
	}
	return d, nil
}

// PruneSetting says whether a fetch also forgets the remote branches the
// server deleted: off unless turned on (gitDir "": for every repo).
func (s *Service) PruneSetting(gitDir string) refs.PruneSetting {
	g, err := s.remoteGit()
	if err != nil {
		return refs.PruneSetting{}
	}
	return refs.NewPruneSetting(g.PruneModes(gitDir))
}

// SetPruneSetting saves "on" or "off" for every repo when gitDir is "",
// else for that repo only, where "" follows the setting for every repo.
func (s *Service) SetPruneSetting(gitDir, value string) error {
	g, err := s.remoteGit()
	if err != nil {
		return err
	}
	v := ""
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true":
		v = "true"
	case "off", "false":
		v = "false"
	case "":
		if gitDir == "" {
			return fmt.Errorf("%q is not a choice: on or off", value)
		}
	default:
		return fmt.Errorf("%q is not a choice: on or off", value)
	}
	where := "every repo"
	if gitDir != "" {
		where = "this repo"
	}
	if s.FS.Dry(fmt.Sprintf("fetching prunes: %q in %s", v, where)) {
		return nil
	}
	return g.SavePruneMode(gitDir, v)
}

// FetchWith is Fetch with prune chosen for this call: it also forgets the
// remote branches the server no longer has, and lists them in Pruned (and
// the local branches that pulled from them in Gone). Only the local copies
// of remote branches go, never a local branch or a file.
func (s *Service) FetchWith(gitDir string, prune bool, progress func(string)) (refs.Fetched, error) {
	f, err := s.fetchWith(gitDir, prune, progress)
	if f.Pruned == nil {
		f.Pruned = []string{}
	}
	if f.Gone == nil {
		f.Gone = []string{}
	}
	return f, err
}

func (s *Service) fetchWith(gitDir string, prune bool, progress func(string)) (refs.Fetched, error) {
	if !prune {
		return s.fetch(gitDir, s.Refs.Fetch, progress)
	}
	g, err := s.remoteGit()
	if err != nil {
		return refs.Fetched{}, err
	}
	before, _ := s.Refs.Branches(gitDir)
	f, err := s.fetch(gitDir, g.FetchPrune, progress)
	if err != nil || s.dry() {
		return f, err
	}
	after, err := s.Refs.Branches(gitDir)
	if err != nil {
		return f, err
	}
	have := map[string]bool{}
	for _, b := range after {
		if b.Remote {
			have[b.Name] = true
		}
	}
	gone := map[string]bool{}
	for _, b := range before {
		if b.Remote && !have[b.Name] {
			f.Pruned = append(f.Pruned, b.Name)
			gone[b.Name] = true
		}
	}
	for _, b := range after {
		if !b.Remote && gone[b.Upstream] {
			f.Gone = append(f.Gone, b.Name)
		}
	}
	return f, nil
}

// pushPlanFor starts a plan, with the reason a force push is not possible
// at all (no remote branch to overwrite); "" when there is none.
func (s *Service) pushPlanFor(gitDir string, h refs.Head) (refs.PushPlan, string) {
	p := refs.PushPlan{Branch: h.Branch, Upstream: h.Upstream, ToOverwrite: []refs.Line{}, ToPush: []refs.Line{}}
	switch {
	case h.Branch == "":
		return p, "HEAD is detached, so there is no branch to push; start one first"
	case h.Upstream == "" && h.Gone != "":
		return p, fmt.Sprintf("%s pulls from %s, which this clone doesn't have (deleted on the server?), so there is nothing to overwrite", h.Branch, h.Gone)
	case h.Upstream == "":
		return p, h.Branch + " is not on a remote yet: a normal push is all it needs (gitswitch push)"
	}
	rem, _ := s.Refs.PushTarget(gitDir, h.Branch)
	if rem == "." {
		return p, fmt.Sprintf("%s follows the local branch %s, not a remote one", h.Branch, h.Upstream)
	}
	p.Remote = rem
	return p, ""
}

// PushPlan shows what a force push with lease would do, from what the last
// fetch brought: the commits on the remote branch that would be overwritten
// (on the remote, not in this branch) and the ones that would go up. It
// reads only; fetch first to see the remote as it is now.
func (s *Service) PushPlan(gitDir string) (refs.PushPlan, error) {
	h, err := s.ready(gitDir)
	if err != nil {
		return refs.PushPlan{}, err
	}
	p, why := s.pushPlanFor(gitDir, h)
	if why != "" {
		p.Why = why
		return p, nil
	}
	// Through @{upstream}: a local branch could be named like the remote one.
	up := h.Branch + "@{upstream}"
	if p.ToOverwrite, err = s.Refs.Commits(gitDir, "refs/heads/"+h.Branch, up, compareLimit); err != nil {
		return p, err
	}
	if p.ToPush, err = s.Refs.Commits(gitDir, up, "refs/heads/"+h.Branch, compareLimit); err != nil {
		return p, err
	}
	p.MoreOverwrite, p.MorePush = max(0, h.Behind-len(p.ToOverwrite)), max(0, h.Ahead-len(p.ToPush))
	p.Expect = s.Refs.Resolve(gitDir, up)
	if s.Repos != nil && s.Accounts != nil {
		p.Account = s.Repos.InfoFor(s.Accounts.Load(), gitDir).Account
	}
	switch {
	case p.Expect == "":
		p.Why = "could not read where " + h.Upstream + " is"
	case h.Behind == 0:
		p.Why = fmt.Sprintf("nothing on %s would be overwritten, so a normal push is all it needs (gitswitch push)", h.Upstream)
	case h.Ahead == 0:
		p.Why = fmt.Sprintf("%s has nothing that %s doesn't have, so pushing would only delete the commits on %s. Update first (gitswitch update)", h.Branch, h.Upstream, h.Upstream)
	default:
		p.CanForce = true
		p.Why = fmt.Sprintf("%s has %s that %s doesn't. A force push with lease replaces them with your %s, and only if %s is still where your last fetch saw it", h.Upstream, plural(h.Behind, "commit"), h.Branch, plural(h.Ahead, "commit"), h.Upstream)
	}
	return p, nil
}

// ForcePushLease is a push that replaces the remote branch's commits with
// this branch's, using git's --force-with-lease and never a plain --force:
// the push goes only if the remote branch is still at the commit the last
// fetch saw (expect, when given, is the one the person was shown: it must be
// that commit, or the push is refused so the list is read again). It is
// only for the current branch, and needs confirmed: the plan's commits were
// shown and the person agreed. Someone pushing since the fetch makes the
// lease fail and nothing changes.
func (s *Service) ForcePushLease(gitDir, expect string, confirmed bool, progress func(string)) (refs.Pushed, error) {
	h, err := s.ready(gitDir)
	r := refs.Pushed{Branch: h.Branch, Upstream: h.Upstream}
	if err != nil {
		return r, err
	}
	g, err := s.remoteGit()
	if err != nil {
		return r, err
	}
	plan, err := s.PushPlan(gitDir)
	if err != nil {
		return r, err
	}
	if !plan.CanForce {
		return r, errors.New(plan.Why)
	}
	if expect = strings.TrimSpace(expect); expect != "" && expect != plan.Expect {
		return r, fmt.Errorf("%s moved since you looked: you saw %s, it is at %s now. Look at the list of commits again before forcing", h.Upstream, short7(expect), short7(plan.Expect))
	}
	r.Remote, r.Account, r.Commits = plan.Remote, plan.Account, h.Ahead
	if !confirmed {
		return r, fmt.Errorf("a force push would overwrite %s on %s and needs your confirmation after you have seen them", plural(h.Behind, "commit"), h.Upstream)
	}
	// The account check (history.CheckAccount) is the caller's: the use
	// cases here don't know the history feature.
	_, merge := s.Refs.PushTarget(gitDir, h.Branch)
	if merge == "" {
		merge = "refs/heads/" + h.Branch
	}
	as := ""
	if plan.Account != "" {
		as = " as " + plan.Account
	}
	if s.FS.Dry(fmt.Sprintf("force push %s to %s%s, only if it is still at %s (git push --force-with-lease=%s:%s %s %s:%s)", h.Branch, h.Upstream, as, short7(plan.Expect), merge, plan.Expect, plan.Remote, h.Branch, merge)) {
		return r, nil
	}
	if err := g.ForcePush(s.Locator.WorktreeOf(gitDir), plan.Remote, "refs/heads/"+h.Branch, merge, plan.Expect, progress); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "stale info") || strings.Contains(low, "[rejected]") || strings.Contains(low, "fetch first") || strings.Contains(low, "updates were rejected") {
			return r, fmt.Errorf("%v\n\nsomeone pushed to %s since you fetched, so nothing was overwritten. Fetch and look at what they added first (gitswitch fetch, then gitswitch push --force-with-lease again)", err, h.Upstream)
		}
		return r, err
	}
	return r, nil
}

// dry is true in dry-run mode.
func (s *Service) dry() bool { return s.FS != nil && s.FS.DryRun }

func short7(h string) string { return h[:min(7, len(h))] }

// guardOrphans refuses to leave a detached HEAD whose own commits no
// branch, tag or remote branch has, unless forced: they would be hard to
// find again.
func (s *Service) guardOrphans(g refs.RemoteGit, gitDir string, h refs.Head, force bool) error {
	if h.Branch != "" || h.Hash == "" || force {
		return nil
	}
	if n := g.Orphans(gitDir); n > 0 {
		return fmt.Errorf("%s you made here (HEAD is detached) %s on no branch, so leaving would hide %s: only the reflog would still find %s. Keep the work first: gitswitch branch new <name>. Or go on anyway with force", plural(n, "commit"), map[bool]string{true: "is", false: "are"}[n == 1], map[bool]string{true: "it", false: "them"}[n == 1], map[bool]string{true: "it", false: "them"}[n == 1])
	}
	return nil
}

// DetachedState says whether HEAD is detached, the branch to go back to,
// and how many commits made here are on no branch.
func (s *Service) DetachedState(gitDir string) (refs.DetachedState, error) {
	h, err := s.Refs.Head(gitDir)
	st := refs.DetachedState{Hash: h.Hash, Short: h.Short}
	if err != nil || h.Branch != "" || h.Hash == "" {
		return st, err
	}
	st.Detached = true
	if g, err := s.remoteGit(); err == nil {
		st.Orphans = g.Orphans(gitDir)
		st.Back = g.DetachedFrom(gitDir)
	}
	if st.Back != "" {
		if all, _ := s.Refs.Branches(gitDir); refs.Find(all, st.Back) == nil {
			st.Back = ""
		}
	}
	st.Words = refs.DetachedWords(h.Short, st.Back)
	return st, nil
}

// CheckoutDetached checks out a commit, a tag or a remote branch as it is
// now, with HEAD on no branch ("detached"). A local branch name is refused
// (switch to it instead). Uncommitted changes are handled as for a switch:
// mode "" lets git carry them only when it can and refuses otherwise,
// "smart" stashes and puts them back (when they clash with the commit,
// nothing changes), "park" leaves them parked on the branch left. The branch
// left is remembered, so Back returns to it. From a detached HEAD with
// commits of your own on no branch it refuses unless force. The switch is
// reported to Undo, which can't undo it (no branch to return to there):
// Back is the way.
func (s *Service) CheckoutDetached(gitDir, rev, mode string, force bool) (refs.Detached, error) {
	rev = strings.TrimSpace(rev)
	d := refs.Detached{Rev: rev}
	g, err := s.remoteGit()
	if err != nil {
		return d, err
	}
	if rev == "" || strings.HasPrefix(rev, "-") {
		return d, fmt.Errorf("not a commit, tag or remote branch: %q", rev)
	}
	switch mode {
	case "", "smart", "park":
	default:
		return d, fmt.Errorf("%q is not a choice: smart or park", mode)
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return d, err
	}
	d.From, d.Carried = h.Branch, h.Changes.Any()
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return d, err
	}
	if b := refs.Find(all, rev); b != nil && !b.Remote {
		return d, fmt.Errorf("%s is a branch: switch to it with gitswitch switch %s. To look at the commit it points at without the branch, name the commit: %s", rev, rev, short7(b.Hash))
	}
	hash, err := s.Refs.Commit(gitDir, rev)
	if err != nil || hash == "" {
		return d, fmt.Errorf("no commit, tag or remote branch %q", rev)
	}
	d.Hash, d.Short = hash, short7(hash)
	if h.Branch == "" && h.Hash == hash {
		return d, fmt.Errorf("HEAD is already at %s", d.Short)
	}
	if err := s.guardOrphans(g, gitDir, h, force); err != nil {
		return d, err
	}
	wt := s.Locator.WorktreeOf(gitDir)
	if mode == "park" && h.Branch == "" && d.Carried {
		return d, errors.New("HEAD is detached, so there is no branch to park the changes on. Take them along with a smart checkout, or commit them first")
	}
	how := ""
	switch mode {
	case "smart":
		how = ", taking the uncommitted changes along"
	case "park":
		how = ", parking the uncommitted changes on " + h.Branch
	}
	if s.FS.Dry(fmt.Sprintf("check out %s (%s) in %s with HEAD detached%s", rev, d.Short, wt, how)) {
		return d, nil
	}
	m := s.before(gitDir)
	switch {
	case mode == "park" && d.Carried:
		stash, err := s.Refs.Stash(wt, refs.ParkMessage(h.Branch))
		if err != nil {
			return d, fmt.Errorf("could not park your changes, nothing changed: %v", err)
		}
		if err := g.Detach(wt, hash); err != nil {
			return d, s.restore(wt, stash, err)
		}
		d.Parked, d.Carried = true, false
	case mode == "smart" && d.Carried:
		err := g.Detach(wt, hash)
		if err != nil && refs.WouldOverwrite(err) {
			if err = s.detachWithStash(g, wt, h, hash, rev); err != nil {
				return d, err
			}
		} else if err != nil {
			return d, err
		}
	default:
		if err := g.Detach(wt, hash); err != nil {
			return d, err
		}
	}
	if h.Branch != "" { // from another detached commit, the branch to go back to stays
		_ = g.SaveDetachedFrom(gitDir, h.Branch)
	}
	d.Back = g.DetachedFrom(gitDir)
	d.Words = refs.DetachedWords(d.Short, d.Back)
	if s.Undo != nil {
		label := "Checked out " + rev + " (detached HEAD)"
		s.Undo.Done(gitDir, m, undo.Switch, label)
	}
	return d, nil
}

// detachWithStash is the smart checkout when git refused to carry the
// changes: stash them, check out, put them back. When they clash with the
// commit it goes back to where it was and puts them back as they were, so
// nothing changes.
func (s *Service) detachWithStash(g refs.RemoteGit, wt string, h refs.Head, hash, rev string) error {
	stash, err := s.Refs.Stash(wt, "gitswitch smart checkout: "+rev)
	if err != nil {
		return fmt.Errorf("could not set your changes aside, nothing changed: %v", err)
	}
	if err := g.Detach(wt, hash); err != nil {
		return s.restore(wt, stash, err)
	}
	files, err := s.Refs.Apply(wt, stash)
	if err == nil && len(files) == 0 {
		return s.Refs.DropStash(wt, stash)
	}
	// Go back to where they were made, then put them back untouched.
	if derr := s.Refs.Discard(wt, stash); derr != nil {
		return fmt.Errorf("your changes could not be put back on %s (%v), and the files could not be cleaned up (%v). They are safe in the stash: `git stash list`", short7(hash), err, derr)
	}
	var back error
	if h.Branch != "" {
		back = s.Refs.Switch(wt, h.Branch)
	} else {
		back = g.Detach(wt, h.Hash)
	}
	if back != nil {
		return fmt.Errorf("your changes clash with %s, and GitSwitch could not go back (%v). They are safe in the stash: `git stash list`", short7(hash), back)
	}
	why := fmt.Errorf("your changes clash with %s in %s, so nothing changed. Park them instead (--park), or commit them first", short7(hash), strings.Join(files, ", "))
	if err != nil && len(files) == 0 {
		why = fmt.Errorf("your changes could not be put back on %s, so nothing changed: %v", short7(hash), err)
	}
	return s.restore(wt, stash, why)
}

// Back leaves a detached HEAD for the branch it was left from (see
// CheckoutDetached), with the same choices for uncommitted changes as a
// switch (mode "", "smart", "park"). Commits made on the detached HEAD that
// no branch has are not lost silently: it refuses unless force. Changes
// parked on the branch come back in Waiting.
func (s *Service) Back(gitDir, mode string, force bool) (refs.Switched, error) {
	g, err := s.remoteGit()
	if err != nil {
		return refs.Switched{}, err
	}
	h, err := s.ready(gitDir)
	if err != nil {
		return refs.Switched{}, err
	}
	if h.Branch != "" {
		return refs.Switched{Branch: h.Branch}, fmt.Errorf("HEAD is on %s, not detached: there is nothing to go back from", h.Branch)
	}
	from := g.DetachedFrom(gitDir)
	if from == "" {
		return refs.Switched{}, errors.New("GitSwitch doesn't know which branch this detached HEAD came from; switch to one: gitswitch switch <branch>")
	}
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return refs.Switched{}, err
	}
	if b := refs.Find(all, from); b == nil || b.Remote {
		return refs.Switched{Branch: from}, fmt.Errorf("the branch %s you came from is gone (deleted or renamed); switch to another: gitswitch switch <branch>", from)
	}
	if err := s.guardOrphans(g, gitDir, h, force); err != nil {
		return refs.Switched{Branch: from}, err
	}
	var r refs.Switched
	switch mode {
	case "smart":
		r, err = s.SmartSwitch(gitDir, from)
	case "park":
		r, err = s.Park(gitDir, from)
	case "":
		var name string
		name, err = s.Switch(gitDir, from)
		r.Branch = name
		if err == nil && !s.dry() {
			r.Waiting = s.waiting(gitDir)
		}
	default:
		return refs.Switched{Branch: from}, fmt.Errorf("%q is not a choice: smart or park", mode)
	}
	if err == nil && r.Clash == nil && !s.dry() {
		_ = g.SaveDetachedFrom(gitDir, "")
	}
	return r, err
}
