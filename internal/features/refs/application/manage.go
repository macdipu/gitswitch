package application

import (
	"errors"
	"fmt"
	"strings"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// DeleteBranch deletes a local branch that is not checked out anywhere.
// When some of its commits are on no other branch or remote it refuses
// with *refs.Unmerged, unless force. Undo brings it back, with the remote
// branch it pulled from.
func (s *Service) DeleteBranch(gitDir, name string, force bool) (refs.DeletedBranch, error) {
	d, err := s.CanDeleteBranch(gitDir, name, force)
	if err != nil {
		return d, err
	}
	name = d.Branch
	what := "delete branch " + name
	if d.Only > 0 {
		what += fmt.Sprintf(" (%s on no other branch: only undo brings them back)", plural(d.Only, "commit"))
	}
	if s.FS.Dry(what) {
		return d, nil
	}
	mark := s.before(gitDir)
	rem, merge := s.Refs.PushTarget(gitDir, name)
	if err := s.Refs.DeleteBranch(gitDir, name, d.Hash); err != nil {
		return d, err
	}
	if s.Undo != nil {
		s.Undo.Pruned(gitDir, mark, []undo.Deleted{{Name: name, Hash: d.Hash, Remote: rem, Merge: merge}}, "Deleted the branch "+name)
	}
	return d, nil
}

// CanDeleteBranch checks what DeleteBranch would check, changing nothing:
// the result says how many commits only the branch has.
func (s *Service) CanDeleteBranch(gitDir, name string, force bool) (refs.DeletedBranch, error) {
	name = strings.TrimSpace(name)
	d := refs.DeletedBranch{Branch: name}
	b, err := s.localBranch(gitDir, name)
	if err != nil {
		return d, err
	}
	d.Hash = b.Hash
	switch {
	case b.Current:
		return d, fmt.Errorf("%s is the current branch, so it can't be deleted; switch to another branch first", name)
	case b.Elsewhere:
		return d, fmt.Errorf("%s is checked out in another working folder (git worktree list), so it can't be deleted; switch that folder to another branch first", name)
	}
	if d.Only, err = s.Refs.Unique(gitDir, name); err != nil {
		return d, err
	}
	if d.Only > 0 && !force {
		return d, &refs.Unmerged{Branch: name, Commits: d.Only}
	}
	return d, nil
}

// RenameBranch renames a local branch (from "" is the current one). It
// keeps pulling from the same remote branch. Undo renames it back.
func (s *Service) RenameBranch(gitDir, from, to string) (refs.Renamed, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	r := refs.Renamed{From: from, To: to}
	if from == "" {
		h, err := s.ready(gitDir)
		if err != nil {
			return r, err
		}
		if h.Branch == "" {
			return r, errors.New("HEAD is detached, so there is no current branch to rename; name the branch to rename")
		}
		from, r.From = h.Branch, h.Branch
	}
	b, err := s.localBranch(gitDir, from)
	if err != nil {
		return r, err
	}
	r.Upstream = b.Upstream
	if to == from {
		return r, fmt.Errorf("%s is already called that", from)
	}
	if err := s.freeName(gitDir, to); err != nil {
		return r, err
	}
	if s.FS.Dry(fmt.Sprintf("rename branch %s to %s (git branch -m)", from, to)) {
		return r, nil
	}
	mark := s.before(gitDir)
	if err := s.Refs.RenameBranch(gitDir, from, to); err != nil {
		return r, err
	}
	if s.Undo != nil {
		s.Undo.Renamed(gitDir, mark, from, to, b.Hash, "Renamed the branch "+from+" to "+to)
	}
	return r, nil
}

// SetUpstream makes a local branch (branch "" is the current one) pull
// from a remote branch, e.g. origin/main; upstream "" makes it pull from
// none (unsetting one that is not set does nothing).
func (s *Service) SetUpstream(gitDir, branch, upstream string) (refs.Upstreamed, error) {
	branch, upstream = strings.TrimSpace(branch), strings.TrimSpace(upstream)
	u := refs.Upstreamed{Branch: branch, Upstream: upstream}
	if branch == "" {
		h, err := s.ready(gitDir)
		if err != nil {
			return u, err
		}
		if h.Branch == "" {
			return u, errors.New("HEAD is detached, so there is no current branch; name the branch")
		}
		branch, u.Branch = h.Branch, h.Branch
	}
	b, err := s.localBranch(gitDir, branch)
	if err != nil {
		return u, err
	}
	u.Was = b.Upstream
	if rem, _ := s.Refs.PushTarget(gitDir, branch); upstream == "" && rem == "" {
		return u, nil // pulls from nothing already
	}
	if upstream != "" {
		all, err := s.Refs.Branches(gitDir)
		if err != nil {
			return u, err
		}
		if strings.HasPrefix(upstream, "-") {
			return u, fmt.Errorf("not a remote branch: %q", upstream)
		}
		if t := refs.Find(all, upstream); t == nil || !t.Remote {
			for _, c := range all {
				if !c.Remote || c.LocalName() != upstream {
					continue
				}
				return u, fmt.Errorf("no remote branch %q; did you mean %s?", upstream, c.Name)
			}
			return u, fmt.Errorf("no remote branch %q (give it as <remote>/<branch>, e.g. origin/%s); if it is new on the server, fetch first: gitswitch fetch", upstream, branch)
		}
	}
	what := fmt.Sprintf("%s pulls from %s (git branch --set-upstream-to)", branch, upstream)
	if upstream == "" {
		what = branch + " pulls from no remote branch (git branch --unset-upstream)"
	}
	if s.FS.Dry(what) {
		return u, nil
	}
	return u, s.Refs.SetUpstream(gitDir, branch, upstream)
}

// localBranch finds a local branch by name, refusing while git is in the
// middle of another operation.
func (s *Service) localBranch(gitDir, name string) (refs.Branch, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return refs.Branch{}, fmt.Errorf("not a branch: %q", name)
	}
	if _, err := s.ready(gitDir); err != nil {
		return refs.Branch{}, err
	}
	all, err := s.Refs.Branches(gitDir)
	if err != nil {
		return refs.Branch{}, err
	}
	b := refs.Find(all, name)
	if b == nil || b.Remote {
		return refs.Branch{}, fmt.Errorf("no local branch %q (see `gitswitch branch`)", name)
	}
	return *b, nil
}

// NewTag tags a commit, branch or tag (rev "" is the current commit):
// annotated when message is not "", else lightweight.
func (s *Service) NewTag(gitDir, name, rev, message string) (refs.TagMade, error) {
	name, rev, message = strings.TrimSpace(name), strings.TrimSpace(rev), strings.TrimSpace(message)
	t := refs.TagMade{Tag: name, Annotated: message != ""}
	if name == "" || strings.HasPrefix(name, "-") || s.Refs.CheckTagName(name) != nil {
		return t, fmt.Errorf("%q can't be a tag name: use letters, digits, - _ . and /, with no spaces, no .. and no ~ ^ : ? * [ or \\", name)
	}
	if s.hasTag(gitDir, name) {
		return t, fmt.Errorf("there is already a tag %s; delete it first, or choose another name", name)
	}
	at := rev
	if rev == "" {
		rev, at = "HEAD", "the current commit"
	}
	if strings.HasPrefix(rev, "-") {
		return t, fmt.Errorf("not a commit: %q", rev)
	}
	hash, err := s.Refs.Commit(gitDir, rev)
	if err != nil || hash == "" {
		if at == "the current commit" {
			return t, errors.New("this repo has no commits yet, so there is nothing to tag")
		}
		return t, fmt.Errorf("no commit, branch or tag %q to tag", rev)
	}
	t.Hash = hash
	kind := "lightweight"
	if t.Annotated {
		kind = "annotated"
	}
	if s.FS.Dry(fmt.Sprintf("tag %s (%s) as %s", at, hash[:min(7, len(hash))], name+", "+kind)) {
		return t, nil
	}
	return t, s.Refs.CreateTag(gitDir, name, hash, message)
}

// DeleteTag deletes a local tag; a copy on a remote stays there.
func (s *Service) DeleteTag(gitDir, name string) error {
	name = strings.TrimSpace(name)
	if err := s.findTag(gitDir, name); err != nil {
		return err
	}
	if s.FS.Dry("delete the tag " + name + " in this clone (git tag -d)") {
		return nil
	}
	return s.Refs.DeleteTag(gitDir, name)
}

// PushTag sends one tag to the remote (origin, or the only remote) through
// the repo's account, never forcing: when the remote has a tag of that
// name at another commit, it refuses and explains. Git's progress lines go
// to progress.
func (s *Service) PushTag(gitDir, name string, progress func(string)) (refs.TagPushed, error) {
	name = strings.TrimSpace(name)
	p := refs.TagPushed{Tag: name}
	if err := s.findTag(gitDir, name); err != nil {
		return p, err
	}
	rem, err := s.pushRemote(gitDir)
	if err != nil {
		return p, err
	}
	p.Remote = rem
	if s.Repos != nil && s.Accounts != nil {
		p.Account = s.Repos.InfoFor(s.Accounts.Load(), gitDir).Account
	}
	as := ""
	if p.Account != "" {
		as = " as " + p.Account
	}
	ref := "refs/tags/" + name
	if s.FS.Dry(fmt.Sprintf("push the tag %s to %s%s (git push %s %s)", name, rem, as, rem, ref)) {
		return p, nil
	}
	if err := s.Refs.Push(s.Locator.WorktreeOf(gitDir), rem, ref, ref, false, progress); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "already exists") || strings.Contains(low, "[rejected]") || strings.Contains(low, "updates were rejected") {
			return p, fmt.Errorf("%v\n\n%s already has a tag %s at another commit, so the push was refused (GitSwitch never forces one). Give this tag another name, or ask who made the other one", err, rem, name)
		}
		return p, err
	}
	return p, nil
}

func (s *Service) hasTag(gitDir, name string) bool {
	tags, _ := s.Refs.Tags(gitDir)
	for _, t := range tags {
		if t == name {
			return true
		}
	}
	return false
}

func (s *Service) findTag(gitDir, name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("not a tag: %q", name)
	}
	if !s.hasTag(gitDir, name) {
		return fmt.Errorf("no tag %q (see `gitswitch tag`)", name)
	}
	return nil
}
