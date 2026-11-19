package application

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	undo "github.com/macdipu/gitswitch/internal/features/undo/domain"
)

// fakeRemoteRefs adds the remote features' port to the branch fake.
type fakeRemoteRefs struct {
	*fakeRefs
	confs      []refs.RemoteConf
	tracking   map[string][]string
	detachErr  error
	detachedAt string
	from       string
	orphans    int
	push       string // the last ForcePush, described
	pushErr    error
	deleteErr  error
	prune      map[string]string
	pruneGone  []string // remote branches the next pruning fetch forgets
	pruneErr   error
}

func (f *fakeRemoteRefs) Remotes(string) ([]refs.RemoteConf, error) { return f.confs, nil }
func (f *fakeRemoteRefs) TrackingBranches(_, r string) []string     { return f.tracking[r] }
func (f *fakeRemoteRefs) AddRemote(_, n, u string) error {
	f.ran = append(f.ran, "remote add "+n+" "+u)
	return nil
}
func (f *fakeRemoteRefs) RemoveRemote(_, n string) error {
	f.ran = append(f.ran, "remote remove "+n)
	return nil
}
func (f *fakeRemoteRefs) RenameRemote(_, a, b string) error {
	f.ran = append(f.ran, "remote rename "+a+" "+b)
	return nil
}
func (f *fakeRemoteRefs) SetRemoteURL(_, n, u string) error {
	f.ran = append(f.ran, "remote set-url "+n+" "+u)
	return nil
}
func (f *fakeRemoteRefs) DeleteRemoteBranch(_, r, b string, _ func(string)) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.ran = append(f.ran, "push "+r+" --delete "+b)
	return nil
}
func (f *fakeRemoteRefs) FetchPrune(_ string, progress func(string)) error {
	f.ran = append(f.ran, "fetch --prune")
	if f.pruneErr != nil {
		return f.pruneErr
	}
	var keep []refs.Branch
	for _, b := range f.branches {
		forgot := false
		for _, g := range f.pruneGone {
			forgot = forgot || b.Remote && b.Name == g
		}
		if !forgot {
			keep = append(keep, b)
		}
	}
	f.branches = keep
	return nil
}
func (f *fakeRemoteRefs) ForcePush(_, rem, src, dst, expect string, _ func(string)) error {
	if f.pushErr != nil {
		return f.pushErr
	}
	f.push = fmt.Sprintf("%s %s:%s lease=%s", rem, src, dst, expect)
	f.ran = append(f.ran, "force-push")
	return nil
}
func (f *fakeRemoteRefs) Detach(_, c string) error {
	if f.detachErr != nil {
		err := f.detachErr
		f.detachErr = nil
		return err
	}
	f.detachedAt = c
	f.ran = append(f.ran, "detach "+c)
	f.head.Branch, f.head.Hash = "", c
	return nil
}
func (f *fakeRemoteRefs) DetachedFrom(string) string { return f.from }
func (f *fakeRemoteRefs) SaveDetachedFrom(_, b string) error {
	f.from = b
	return nil
}
func (f *fakeRemoteRefs) Orphans(string) int { return f.orphans }
func (f *fakeRemoteRefs) PruneModes(g string) (string, string) {
	return f.prune[""], map[bool]string{true: f.prune[g]}[g != ""]
}
func (f *fakeRemoteRefs) SavePruneMode(g, v string) error {
	if f.prune == nil {
		f.prune = map[string]string{}
	}
	f.prune[g] = v
	return nil
}
func (f *fakeRemoteRefs) Resolve(_, rev string) string {
	if strings.HasSuffix(rev, "@{upstream}") {
		return "1111111aaaa"
	}
	return ""
}

func newRemoteService() (*Service, *fakeRemoteRefs) {
	s, base := newService()
	base.remotes = []string{"origin", "up"}
	base.target = [2]string{"origin", "refs/heads/main"}
	base.defBranch = "main"
	f := &fakeRemoteRefs{fakeRefs: base,
		confs: []refs.RemoteConf{
			{Name: "origin", FetchURL: "git@github.com:acme/api.git", PushURL: "git@github.com:acme/api.git"},
			{Name: "up", FetchURL: "https://github.com/other/api.git", PushURL: "https://github.com/other/api.git"},
		},
		tracking: map[string][]string{"origin": {"fix", "main"}}}
	s.Refs = f
	return s, f
}

// ownerRepos reports the repo as saved with account "work", whose owner
// rule maps acme to "work" and other to "me".
type ownerRepos struct{ repo.Reader }

func (ownerRepos) InfoFor(*account.Config, string) repo.Info {
	return repo.Info{Account: "work", Saved: "work"}
}

type ownerAccounts struct{ account.Store }

func (ownerAccounts) Load() *account.Config {
	return &account.Config{
		Accounts: []account.Account{{ID: "work", Host: "github.com"}, {ID: "me", Host: "github.com"}},
		Rules:    []account.Rule{{Kind: "owner", Pattern: "acme", Account: "work"}, {Kind: "owner", Pattern: "other", Account: "me"}},
	}
}

func TestRemotesList(t *testing.T) {
	s, _ := newRemoteService()
	s.Repos, s.Accounts = ownerRepos{}, ownerAccounts{}
	rs, err := s.Remotes("/r/.git")
	if err != nil || len(rs) != 2 {
		t.Fatalf("%v %v", rs, err)
	}
	o, u := rs[0], rs[1]
	if o.Name != "origin" || !o.Upstream || o.Account != "work" || o.Host != "github.com" || strings.Join(o.Tracking, ",") != "fix,main" {
		t.Errorf("origin: %+v", o)
	}
	// The repo is saved as work, so every remote of it fetches as work.
	if u.Upstream || u.Account != "work" || len(u.Tracking) != 0 || u.Tracking == nil {
		t.Errorf("up: %+v", u)
	}
	s.Refs = r0{} // no port: no remote features
	if _, err := s.Remotes("/r/.git"); err == nil {
		t.Error("a Refs without the port has no remotes")
	}
}

type r0 struct{ refs.Refs }

func TestAddRemote(t *testing.T) {
	s, f := newRemoteService()
	s.Repos, s.Accounts = ownerRepos{}, ownerAccounts{}
	for _, c := range []struct{ name, url, want string }{
		{"origin", "git@github.com:x/y.git", "already a remote called origin"},
		{"bad name", "git@github.com:x/y.git", "can't be a remote name"},
		{"", "git@github.com:x/y.git", "can't be a remote name"},
		{"fork", "/tmp/local", "not a repo URL"},
		{"fork", "file:///tmp/x", "not supported"},
		{"fork", "-oProxyCommand=x", "not a repo URL"},
		{"fork", "ext::sh -c x", "not a repo URL"},
	} {
		if _, err := s.AddRemote("/r/.git", c.name, c.url); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q %q: %v, want %q", c.name, c.url, err, c.want)
		}
	}
	if len(f.ran) != 0 {
		t.Fatalf("refused, yet ran %v", f.ran)
	}
	// An owner that your rules give to another account than the repo's saved one.
	c, err := s.AddRemote("/r/.git", "fork", " git@github.com:other/api.git ")
	if err != nil || c.URL != "git@github.com:other/api.git" || c.Account != "work" || c.Host != "github.com" ||
		!strings.Contains(c.Note, "account me") || !strings.Contains(c.Note, "saved as work") || !strings.Contains(c.Note, "gitswitch use me") {
		t.Fatalf("add: %+v %v", c, err)
	}
	if strings.Join(f.ran, ",") != "remote add fork git@github.com:other/api.git" {
		t.Errorf("ran %v", f.ran)
	}
	// The same owner as the saved account: nothing to say.
	if c, err := s.AddRemote("/r/.git", "mirror", "https://github.com/acme/api2.git"); err != nil || c.Note != "" || c.Account != "work" {
		t.Errorf("same owner: %+v %v", c, err)
	}
}

func TestRemoveRenameSetURL(t *testing.T) {
	s, f := newRemoteService()
	// Branches track origin: refused, with the names, unless forced.
	c, err := s.RemoveRemote("/r/.git", "origin", false)
	if err == nil || !strings.Contains(err.Error(), "fix, main pull from origin") || strings.Join(c.Branches, ",") != "fix,main" || len(f.ran) != 0 {
		t.Fatalf("remove tracked: %+v %v %v", c, err, f.ran)
	}
	c, err = s.RemoveRemote("/r/.git", "origin", true)
	if err != nil || c.Was != "git@github.com:acme/api.git" || !strings.Contains(c.Note, "remote add origin git@github.com:acme/api.git") || strings.Join(f.ran, ",") != "remote remove origin" {
		t.Fatalf("forced: %+v %v %v", c, err, f.ran)
	}
	f.ran = nil
	if _, err := s.RemoveRemote("/r/.git", "up", false); err != nil || strings.Join(f.ran, ",") != "remote remove up" {
		t.Errorf("an untracked remote goes: %v %v", err, f.ran)
	}
	if _, err := s.RemoveRemote("/r/.git", "nope", false); err == nil || !strings.Contains(err.Error(), "its remotes are origin, up") {
		t.Errorf("unknown: %v", err)
	}

	f.ran = nil
	for _, c := range [][3]string{{"origin", "up", "already a remote called up"}, {"origin", "origin", "same as the old one"}, {"origin", "a b", "can't be a remote name"}, {"nope", "x", "no remote called"}} {
		if _, err := s.RenameRemote("/r/.git", c[0], c[1]); err == nil || !strings.Contains(err.Error(), c[2]) {
			t.Errorf("rename %v: %v", c, err)
		}
	}
	r, err := s.RenameRemote("/r/.git", "origin", "main-remote")
	if err != nil || !strings.Contains(r.Note, "fix, main now track main-remote") || strings.Join(f.ran, ",") != "remote rename origin main-remote" {
		t.Errorf("rename: %+v %v %v", r, err, f.ran)
	}

	f.ran = nil
	if _, err := s.SetRemoteURL("/r/.git", "origin", "git@github.com:acme/api.git"); err == nil || !strings.Contains(err.Error(), "already points") {
		t.Errorf("same url: %v", err)
	}
	if _, err := s.SetRemoteURL("/r/.git", "origin", "/etc/passwd"); err == nil {
		t.Error("a path is not a URL")
	}
	f.confs[0].PushURL = "git@github.com:acme/push.git"
	u, err := s.SetRemoteURL("/r/.git", "origin", "git@github.com:acme/new.git")
	if err != nil || u.Was != "git@github.com:acme/api.git" || !strings.Contains(u.Note, "its own push URL") || strings.Join(f.ran, ",") != "remote set-url origin git@github.com:acme/new.git" {
		t.Errorf("set-url: %+v %v %v", u, err, f.ran)
	}
}

func TestDeleteRemoteBranch(t *testing.T) {
	s, f := newRemoteService()
	s.Repos, s.Accounts = pushRepos{}, noAccounts{}
	f.branches = append(f.branches, refs.Branch{Name: "origin/old", Remote: true, Hash: "9999999deadbeef"}, refs.Branch{Name: "fix", Upstream: "origin/old"})
	f.head.Upstream = "origin/main"
	progress := func(string) {}
	for ref, want := range map[string]string{
		"origin/main": "default branch", "origin/ghost": "no remote branch", "old": "not a remote branch", "-x/y": "not a remote branch",
		"origin/fix": "",
	} {
		_, err := s.DeleteRemoteBranch("/r/.git", ref, false, progress)
		if want == "" {
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", ref, err, want)
		}
	}
	// The branch you are on pulls from it.
	f.head.Upstream = "origin/fix"
	if _, err := s.DeleteRemoteBranch("/r/.git", "origin/fix", false, progress); err == nil || !strings.Contains(err.Error(), "you are on main") {
		t.Errorf("current: %v", err)
	}
	f.head.Upstream = "origin/main"
	f.ran = nil
	d, err := s.DeleteRemoteBranch("/r/.git", "origin/old", false, progress)
	if err != nil || d.Remote != "origin" || d.Branch != "old" || d.Account != "work" || strings.Join(d.Tracking, ",") != "fix" ||
		d.Restore != "git push origin 9999999deadbeef:refs/heads/old" || strings.Join(f.ran, ",") != "push origin --delete old" {
		t.Fatalf("delete: %+v %v %v", d, err, f.ran)
	}
	// Forced, the default branch goes too.
	if _, err := s.DeleteRemoteBranch("/r/.git", "origin/main", true, progress); err != nil {
		t.Errorf("forced: %v", err)
	}
	// Remote branches with slashes, and a branch that is already gone on the server.
	f.branches = append(f.branches, refs.Branch{Name: "origin/feature/x", Remote: true, Hash: "abc"})
	f.deleteErr = errors.New("error: unable to delete 'feature/x': remote ref does not exist")
	if d, err := s.DeleteRemoteBranch("/r/.git", "origin/feature/x", false, progress); err == nil || d.Branch != "feature/x" || !strings.Contains(err.Error(), "fetch --prune") {
		t.Errorf("gone: %+v %v", d, err)
	}
	// Without a known default branch, main and master are protected.
	f.defBranch = ""
	f.branches = append(f.branches, refs.Branch{Name: "origin/master", Remote: true})
	if _, err := s.DeleteRemoteBranch("/r/.git", "origin/master", false, progress); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Errorf("master: %v", err)
	}
}

func TestFetchWithPrune(t *testing.T) {
	s, f := newRemoteService()
	f.head = refs.Head{Branch: "main", Hash: "abc", Upstream: "origin/main"}
	f.branches = []refs.Branch{{Name: "main", Current: true, Upstream: "origin/main"}, {Name: "fix", Upstream: "origin/fix"},
		{Name: "origin/main", Remote: true}, {Name: "origin/fix", Remote: true}, {Name: "origin/keep", Remote: true}}
	f.pruneGone = []string{"origin/fix"}
	// Off by default: a plain fetch.
	if _, err := s.Fetch("/r/.git", func(string) {}); err != nil || strings.Join(f.ran, ",") != "fetch" {
		t.Fatalf("default: %v %v", err, f.ran)
	}
	f.ran = nil
	r, err := s.FetchWith("/r/.git", true, func(string) {})
	if err != nil || strings.Join(f.ran, ",") != "fetch --prune" || strings.Join(r.Pruned, ",") != "origin/fix" || strings.Join(r.Gone, ",") != "fix" {
		t.Fatalf("prune: %+v %v %v", r, err, f.ran)
	}
	// The setting turns it on for plain fetches, per repo or for every repo.
	f.ran = nil
	if err := s.SetPruneSetting("", "on"); err != nil || !s.PruneSetting("/r/.git").On {
		t.Fatalf("on: %v", err)
	}
	if err := s.SetPruneSetting("/r/.git", "off"); err != nil || s.PruneSetting("/r/.git").On || !s.PruneSetting("").On {
		t.Errorf("repo off: %+v %+v", s.PruneSetting("/r/.git"), s.PruneSetting(""))
	}
	if err := s.SetPruneSetting("/r/.git", ""); err != nil || !s.PruneSetting("/r/.git").On {
		t.Errorf("repo follows every repo again: %+v", s.PruneSetting("/r/.git"))
	}
	if _, err := s.Fetch("/r/.git", func(string) {}); err != nil || strings.Join(f.ran, ",") != "fetch --prune" {
		t.Errorf("setting on: %v %v", err, f.ran)
	}
	for _, bad := range [][2]string{{"", "maybe"}, {"", ""}, {"/r/.git", "x"}} {
		if err := s.SetPruneSetting(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	// A failed fetch says why and reports nothing pruned.
	f.pruneErr = errors.New("timed out")
	if r, err := s.FetchWith("/r/.git", true, func(string) {}); err == nil || len(r.Pruned) != 0 {
		t.Errorf("failed: %+v %v", r, err)
	}
}

func forceService() (*Service, *fakeRemoteRefs) {
	s, f := newRemoteService()
	s.Repos, s.Accounts = pushRepos{}, noAccounts{}
	f.head = refs.Head{Branch: "main", Hash: "abc", Upstream: "origin/main", Ahead: 2, Behind: 1}
	f.lines = map[string][]refs.Line{
		"refs/heads/main..main@{upstream}": {{Hash: "r1", Short: "r1", Subject: "theirs"}},
		"main@{upstream}..refs/heads/main": {{Short: "l2", Subject: "mine 2"}, {Short: "l1", Subject: "mine 1"}},
	}
	return s, f
}

func TestPushPlan(t *testing.T) {
	s, f := forceService()
	p, err := s.PushPlan("/r/.git")
	if err != nil || !p.CanForce || p.Remote != "origin" || p.Upstream != "origin/main" || p.Expect != "1111111aaaa" || p.Account != "work" ||
		len(p.ToOverwrite) != 1 || p.ToOverwrite[0].Subject != "theirs" || len(p.ToPush) != 2 || !strings.Contains(p.Why, "1 commit that main doesn't") {
		t.Fatalf("plan: %+v %v", p, err)
	}
	for name, c := range map[string]struct {
		head refs.Head
		want string
	}{
		"nothing to overwrite": {refs.Head{Branch: "main", Hash: "a", Upstream: "origin/main", Ahead: 2}, "a normal push is all it needs"},
		"only behind":          {refs.Head{Branch: "main", Hash: "a", Upstream: "origin/main", Behind: 2}, "only delete the commits"},
		"detached":             {refs.Head{Hash: "a"}, "HEAD is detached"},
		"no upstream":          {refs.Head{Branch: "main", Hash: "a"}, "not on a remote yet"},
		"gone":                 {refs.Head{Branch: "main", Hash: "a", Gone: "origin/main"}, "doesn't have"},
		"busy":                 {refs.Head{Branch: "main", Hash: "a", Upstream: "origin/main", Ahead: 1, Behind: 1, Busy: "rebase"}, "rebase"},
	} {
		f.head = c.head
		p, err := s.PushPlan("/r/.git")
		reason := p.Why
		if err != nil {
			reason = err.Error()
		}
		if p.CanForce || !strings.Contains(reason, c.want) {
			t.Errorf("%s: %+v %v", name, p, err)
		}
	}
	f.head = refs.Head{Branch: "main", Hash: "a", Upstream: "main", Ahead: 1, Behind: 1}
	f.target = [2]string{".", "refs/heads/main"}
	if p, _ := s.PushPlan("/r/.git"); p.CanForce || !strings.Contains(p.Why, "local branch") {
		t.Errorf("local upstream: %+v", p)
	}
}

func TestForcePushLease(t *testing.T) {
	s, f := forceService()
	progress := func(string) {}
	// Not confirmed: nothing runs, and it says what is at stake.
	if _, err := s.ForcePushLease("/r/.git", "", false, progress); err == nil || !strings.Contains(err.Error(), "confirmation") || f.push != "" {
		t.Fatalf("unconfirmed: %v %q", err, f.push)
	}
	// The remote moved since the list the person saw.
	if _, err := s.ForcePushLease("/r/.git", "2222222bbbb", true, progress); err == nil || !strings.Contains(err.Error(), "moved since you looked") || f.push != "" {
		t.Fatalf("stale list: %v %q", err, f.push)
	}
	r, err := s.ForcePushLease("/r/.git", "1111111aaaa", true, progress)
	if err != nil || r.Commits != 2 || r.Account != "work" || r.Upstream != "origin/main" || f.push != "origin refs/heads/main:refs/heads/main lease=1111111aaaa" {
		t.Fatalf("push: %+v %v %q", r, err, f.push)
	}
	// Never a plain force: the lease is always there, and an empty expect uses the one just read.
	f.push = ""
	if _, err := s.ForcePushLease("/r/.git", "", true, progress); err != nil || !strings.Contains(f.push, "lease=1111111aaaa") {
		t.Errorf("default expect: %v %q", err, f.push)
	}
	// The lease fails: someone pushed since the fetch.
	f.pushErr = errors.New(" ! [rejected]        main -> main (stale info)")
	if _, err := s.ForcePushLease("/r/.git", "", true, progress); err == nil || !strings.Contains(err.Error(), "someone pushed to origin/main since you fetched") || !strings.Contains(err.Error(), "fetch") {
		t.Errorf("lease: %v", err)
	}
	f.pushErr = errors.New("ssh: connect to host github.com port 22: Operation timed out")
	if _, err := s.ForcePushLease("/r/.git", "", true, progress); err == nil || strings.Contains(err.Error(), "someone pushed") {
		t.Errorf("another failure keeps its words: %v", err)
	}
	// A normal push would do: no force.
	f.head.Behind, f.pushErr, f.push = 0, nil, ""
	if _, err := s.ForcePushLease("/r/.git", "", true, progress); err == nil || f.push != "" {
		t.Errorf("nothing to overwrite: %v %q", err, f.push)
	}
}

type detachRec struct {
	fakeRecorder
	label string
	a     undo.Action
}

func (d *detachRec) Done(_ string, _ undo.Mark, a undo.Action, label string) { d.a, d.label = a, label }

func detachService() (*Service, *fakeRemoteRefs, *detachRec) {
	s, f := newRemoteService()
	f.head = refs.Head{Branch: "main", Hash: "main-hash", Short: "main-ha"}
	f.branches = []refs.Branch{{Name: "main", Current: true, Hash: "main-hash"}, {Name: "fix", Hash: "fix-hash"}, {Name: "origin/fix", Remote: true, Hash: "fix-hash"}}
	rec := &detachRec{}
	s.Undo = rec
	return s, f, rec
}

func TestCheckoutDetached(t *testing.T) {
	s, f, rec := detachService()
	for rev, want := range map[string]string{"": "not a commit", "-x": "not a commit", "nope": "no commit, tag or remote branch", "fix": "is a branch: switch to it"} {
		if _, err := s.CheckoutDetached("/r/.git", rev, "", false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", rev, err, want)
		}
	}
	if _, err := s.CheckoutDetached("/r/.git", "v1", "wat", false); err == nil || !strings.Contains(err.Error(), "smart or park") {
		t.Errorf("mode: %v", err)
	}
	if len(f.ran) != 0 {
		t.Fatalf("refused, yet ran %v", f.ran)
	}
	d, err := s.CheckoutDetached("/r/.git", "v1", "", false)
	if err != nil || d.Hash != "hash-of-v1" || d.From != "main" || d.Back != "main" || f.from != "main" || !strings.Contains(d.Words, "not on a branch") ||
		strings.Join(f.ran, ",") != "detach hash-of-v1" || rec.a != undo.Switch || !strings.Contains(rec.label, "v1") {
		t.Fatalf("detach: %+v %v %v %v", d, err, f.ran, rec)
	}
	// A remote branch works as the commit it is at.
	f.head = refs.Head{Branch: "main", Hash: "main-hash"}
	f.ran = nil
	if d, err := s.CheckoutDetached("/r/.git", "origin/fix", "", false); err != nil || d.Rev != "origin/fix" || strings.Join(f.ran, ",") != "detach hash-of-origin/fix" {
		t.Errorf("remote branch: %+v %v %v", d, err, f.ran)
	}
	// From a detached HEAD the branch to go back to stays.
	f.head = refs.Head{Hash: "x", Short: "x"}
	if _, err := s.CheckoutDetached("/r/.git", "v2", "", false); err != nil || f.from != "main" {
		t.Errorf("detached to detached: %v from=%q", err, f.from)
	}
	f.head = refs.Head{Hash: "hash-of-v9"}
	if _, err := s.CheckoutDetached("/r/.git", "v9", "", false); err == nil || !strings.Contains(err.Error(), "already at") {
		t.Errorf("same commit: %v", err)
	}
}

func TestCheckoutDetachedWithChanges(t *testing.T) {
	s, f, _ := detachService()
	f.head.Changes = refs.Changes{Changed: 2}
	// Plain: git's refusal comes through, nothing else happens.
	f.detachErr = errors.New("error: Your local changes to the following files would be overwritten by checkout")
	if _, err := s.CheckoutDetached("/r/.git", "v1", "", false); err == nil || !strings.Contains(err.Error(), "would be overwritten") || len(f.ran) != 0 {
		t.Fatalf("plain: %v %v", err, f.ran)
	}
	// Smart: git refuses, so stash, check out, put them back.
	f.detachErr = errors.New("error: Your local changes would be overwritten by checkout")
	d, err := s.CheckoutDetached("/r/.git", "v1", "smart", false)
	if err != nil || !d.Carried || strings.Join(f.ran, ",") != "stash,detach hash-of-v1,apply st,drop st" {
		t.Fatalf("smart: %+v %v %v", d, err, f.ran)
	}
	// Smart when they clash with the commit: back on the branch, changes as they were.
	f.head = refs.Head{Branch: "main", Hash: "main-hash", Changes: refs.Changes{Changed: 1}}
	f.ran, f.from = nil, ""
	f.detachErr = errors.New("error: Your local changes would be overwritten by checkout")
	f.applyFiles = []string{"a.txt"}
	_, err = s.CheckoutDetached("/r/.git", "v1", "smart", false)
	if err == nil || !strings.Contains(err.Error(), "nothing changed") || !strings.Contains(err.Error(), "a.txt") ||
		strings.Join(f.ran, ",") != "stash,detach hash-of-v1,apply st,discard,switch main,apply st,drop st" {
		t.Fatalf("clash: %v %v", err, f.ran)
	}
	// Park: the changes are stashed under the branch's name.
	f.head = refs.Head{Branch: "main", Hash: "main-hash", Changes: refs.Changes{Changed: 1}}
	f.ran = nil
	d, err = s.CheckoutDetached("/r/.git", "v1", "park", false)
	if err != nil || !d.Parked || f.stashMsg != refs.ParkMessage("main") || strings.Join(f.ran, ",") != "stash,detach hash-of-v1" {
		t.Fatalf("park: %+v %v %v %q", d, err, f.ran, f.stashMsg)
	}
	// A failed detach puts parked changes back.
	f.head = refs.Head{Branch: "main", Hash: "main-hash", Changes: refs.Changes{Changed: 1}}
	f.ran = nil
	f.detachErr = errors.New("boom")
	if _, err := s.CheckoutDetached("/r/.git", "v1", "park", false); err == nil || strings.Join(f.ran, ",") != "stash,apply st,drop st" {
		t.Errorf("park failure: %v %v", err, f.ran)
	}
	// There is no branch to park on while detached.
	f.head = refs.Head{Hash: "x", Changes: refs.Changes{Changed: 1}}
	if _, err := s.CheckoutDetached("/r/.git", "v1", "park", false); err == nil || !strings.Contains(err.Error(), "no branch to park") {
		t.Errorf("detached park: %v", err)
	}
}

func TestDetachedKeepsOwnCommits(t *testing.T) {
	s, f, _ := detachService()
	f.head = refs.Head{Hash: "x", Short: "x"}
	f.from, f.orphans = "main", 2
	for _, err := range []error{
		func() error { _, err := s.CheckoutDetached("/r/.git", "v1", "", false); return err }(),
		func() error { _, err := s.Back("/r/.git", "", false); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), "2 commits you made here") || !strings.Contains(err.Error(), "branch new") {
			t.Errorf("orphans: %v", err)
		}
	}
	if len(f.ran) != 0 {
		t.Fatalf("refused, yet ran %v", f.ran)
	}
	if _, err := s.Back("/r/.git", "", true); err != nil {
		t.Errorf("forced: %v", err)
	}
}

func TestBack(t *testing.T) {
	s, f, _ := detachService()
	if _, err := s.Back("/r/.git", "", false); err == nil || !strings.Contains(err.Error(), "not detached") {
		t.Errorf("on a branch: %v", err)
	}
	f.head = refs.Head{Hash: "x", Short: "x"}
	if _, err := s.Back("/r/.git", "", false); err == nil || !strings.Contains(err.Error(), "doesn't know which branch") {
		t.Errorf("no record: %v", err)
	}
	f.from = "gone"
	if _, err := s.Back("/r/.git", "", false); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Errorf("branch gone: %v", err)
	}
	st, _ := s.DetachedState("/r/.git")
	if !st.Detached || st.Back != "" || st.Hash != "x" {
		t.Errorf("state without a branch to return to: %+v", st)
	}
	f.from = "fix"
	if st, _ := s.DetachedState("/r/.git"); st.Back != "fix" || !strings.Contains(st.Words, "go back to fix") {
		t.Errorf("state: %+v", st)
	}
	r, err := s.Back("/r/.git", "", false)
	if err != nil || r.Branch != "fix" || f.from != "" || strings.Join(f.ran, ",") != "switch fix" {
		t.Fatalf("back: %+v %v %v from=%q", r, err, f.ran, f.from)
	}
	if st, _ := s.DetachedState("/r/.git"); st.Back != "" {
		t.Errorf("way back forgotten once taken: %+v", st)
	}
	f.head, f.from, f.ran = refs.Head{Hash: "x", Changes: refs.Changes{Changed: 1}}, "fix", nil
	if _, err := s.Back("/r/.git", "park", false); err == nil || !strings.Contains(err.Error(), "no branch to park") {
		t.Errorf("park from detached: %v", err)
	}
	if _, err := s.Back("/r/.git", "x", false); err == nil {
		t.Error("bad mode accepted")
	}
	// A failed switch keeps the way back.
	f.head.Changes = refs.Changes{}
	f.switchErrs = []error{errors.New("boom")}
	if _, err := s.Back("/r/.git", "", false); err == nil || f.from != "fix" {
		t.Errorf("failed back: %v from=%q", err, f.from)
	}
}
