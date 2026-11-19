package application

import (
	"fmt"
	"strings"

	inspect "github.com/macdipu/gitswitch/internal/features/inspect/domain"
)

// DefaultReflog and MaxPage are the page sizes of the reflog.
const (
	DefaultReflog = 30
	MaxPage       = 200
)

// Reflog reads a page of ref's reflog ("" or HEAD for HEAD's), n entries
// after skip newer ones, each marked lost when its commit is on no branch,
// tag or remote branch. With lostOnly only those are listed; it looks at
// at most MaxReflog entries per call and Next says where to go on.
func (s *Service) Reflog(gitDir, ref string, n, skip int, lostOnly bool) (inspect.Reflogged, error) {
	ref, err := inspect.CheckReflogRef(ref)
	r := inspect.Reflogged{Ref: ref, Entries: []inspect.Entry{}}
	if err != nil {
		return r, err
	}
	if n <= 0 {
		n = DefaultReflog
	}
	n = min(n, MaxPage)
	skip = max(skip, 0)
	r.Next = skip
	scanned := 0
	for len(r.Entries) < n && scanned < inspect.MaxReflog {
		want := n + 1 // one more tells whether there is a next page
		if lostOnly {
			want = min(100, inspect.MaxReflog-scanned)
		}
		es, err := s.Git.Reflog(gitDir, ref, r.Next, want)
		if err != nil {
			return r, err
		}
		if len(es) == 0 {
			return r, nil
		}
		lost := s.Git.Unreachable(gitDir, hashes(es))
		for _, e := range es {
			if len(r.Entries) == n {
				r.More = true // an entry beyond the page
				return r, nil
			}
			e.Lost = lost[e.Hash]
			r.Next = e.Index + 1
			scanned++
			if lostOnly && !e.Lost {
				continue
			}
			r.Entries = append(r.Entries, e)
		}
		if len(es) < want {
			return r, nil // the reflog ends
		}
	}
	// Stopped by the page being full or by the scan limit: there may be more.
	r.More = true
	return r, nil
}

func hashes(es []inspect.Entry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range es {
		if !seen[e.Hash] {
			seen[e.Hash] = true
			out = append(out, e.Hash)
		}
	}
	return out
}

// Recover makes a branch called name at a reflog entry (index, counting
// from the newest; or hash when index < 0) of ref. It never moves or
// switches the current branch and never touches files; delete the branch
// to take it back.
func (s *Service) Recover(gitDir, ref string, index int, hash, name string) (inspect.Recovered, error) {
	name, hash = strings.TrimSpace(name), strings.TrimSpace(hash)
	r := inspect.Recovered{Name: name}
	if name == "" || strings.HasPrefix(name, "-") || s.Git.CheckName(name) != nil {
		return r, fmt.Errorf("%q can't be a branch name: use letters, digits, - _ . and /, with no spaces, no .. and no ~ ^ : ? * [ or \\", name)
	}
	if s.Git.BranchExists(gitDir, name) {
		return r, fmt.Errorf("a branch called %s already exists; pick another name", name)
	}
	if index >= 0 {
		ref, err := inspect.CheckReflogRef(ref)
		if err != nil {
			return r, err
		}
		es, err := s.Git.Reflog(gitDir, ref, index, 1)
		if err != nil {
			return r, err
		}
		if len(es) == 0 {
			return r, fmt.Errorf("the reflog has no entry %d", index)
		}
		hash = es[0].Hash
	} else if hash == "" {
		return r, fmt.Errorf("name the reflog entry (its number) or the commit to start the branch at")
	} else if strings.HasPrefix(hash, "-") {
		return r, fmt.Errorf("not a commit: %q", hash)
	}
	full, err := s.Git.Resolve(gitDir, hash)
	if err != nil {
		return r, fmt.Errorf("no commit %q in this repo", hash)
	}
	c, err := s.Git.Info(gitDir, full)
	if err != nil {
		return r, err
	}
	r.Hash, r.Short, r.Subject = full, c.Short, c.Subject
	r.Lost = s.Git.Unreachable(gitDir, []string{full})[full]
	if s.FS.Dry(fmt.Sprintf("make branch %s at %s %s (the current branch stays as it is)", name, c.Short, c.Subject)) {
		return r, nil
	}
	if err := s.Git.CreateBranch(gitDir, name, full); err != nil {
		return r, err
	}
	r.Message = fmt.Sprintf("Made branch %s at %s %s. You are still where you were; switch with: gitswitch switch %s. To take it back, delete it: gitswitch branch delete %s.", name, c.Short, c.Subject, name, name)
	return r, nil
}
