package domain

import (
	"reflect"
	"strings"
	"testing"

	refs "github.com/macdipu/gitswitch/internal/features/refs/domain"
)

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": SinceFork, "since-fork": SinceFork, "three-dot": SinceFork, "direct": Direct, "Two-Dot": Direct} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseMode("both"); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestCheckSide(t *testing.T) {
	for _, c := range []struct {
		ref    string
		second bool
		ok     bool
	}{
		{"main", false, true}, {"v1.2", true, true}, {"HEAD~3", false, true}, {"abc1234", true, true},
		{"", false, false}, {"  ", true, false}, {"--output=x", false, false}, {"a b", true, false},
		{Working, true, true}, {Working, false, false},
	} {
		if err := CheckSide(c.ref, c.second); (err == nil) != c.ok {
			t.Errorf("CheckSide(%q, %v) = %v", c.ref, c.second, err)
		}
	}
}

func TestParseChanges(t *testing.T) {
	names := "M\x00a.txt\x00A\x00dir/b.txt\x00R100\x00old.txt\x00new.txt\x00D\x00gone.txt\x00T\x00link\x00C75\x00s.txt\x00copy.txt\x00"
	nums := "3\t1\ta.txt\x005\t0\tdir/b.txt\x000\t0\t\x00old.txt\x00new.txt\x000\t7\tgone.txt\x00-\t-\tlink\x001\t1\t\x00s.txt\x00copy.txt\x00"
	files := JoinChanges(ParseNameStatus(names), ParseNumstat(nums))
	want := []FileChange{
		{Path: "a.txt", Status: "modified", Added: 3, Deleted: 1},
		{Path: "dir/b.txt", Status: "added", Added: 5},
		{Path: "new.txt", OldPath: "old.txt", Status: "renamed"},
		{Path: "gone.txt", Status: "deleted", Deleted: 7},
		{Path: "link", Status: "type changed", Binary: true},
		{Path: "copy.txt", OldPath: "s.txt", Status: "copied", Added: 1, Deleted: 1},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("files = %+v\nwant %+v", files, want)
	}
	if got := ParseNameStatus("R100\x00only-one\x00"); len(got) != 0 {
		t.Errorf("a cut rename should be dropped: %+v", got)
	}
	if got := ParseNameStatus(""); got != nil {
		t.Errorf("empty = %+v", got)
	}
}

func TestFinishAndSummary(t *testing.T) {
	var many []FileChange
	for i := 0; i < MaxFiles+3; i++ {
		many = append(many, FileChange{Path: "f", Added: 1})
	}
	for _, c := range []struct {
		name string
		c    Compared
		want []string
	}{
		{"same", Compared{A: "main", B: "dev", Mode: SinceFork}, []string{"main and dev are the same."}},
		{"since fork", Compared{A: "main", B: "dev", Mode: SinceFork, OnlyBCount: 2, Files: []FileChange{{Added: 4, Deleted: 1}}},
			[]string{"dev has 2 commits that main doesn't", "Since they went apart, dev changed 1 file (+4 -1)"}},
		{"direct", Compared{A: "main", B: "dev", Mode: Direct, OnlyACount: 1, OnlyBCount: 1, Files: []FileChange{{Added: 1}, {Deleted: 2}}},
			[]string{"main has 1 commit that dev doesn't", "dev has 1 commit that main doesn't", "The two differ in 2 files (+1 -2)"}},
		{"working", Compared{A: "main", B: Working, Working: true, Mode: Direct, Files: []FileChange{{Added: 1}}},
			[]string{"your working folder", "same commits"}},
		{"cap", Compared{A: "a", B: "b", Mode: Direct, Files: many}, []string{"503 files"}},
	} {
		c.c.Finish()
		for _, w := range c.want {
			if !strings.Contains(c.c.Summary, w) {
				t.Errorf("%s: summary %q lacks %q", c.name, c.c.Summary, w)
			}
		}
	}
	m := Compared{Files: many}
	m.Finish()
	if len(m.Files) != MaxFiles || m.MoreFiles != 3 || m.Added != MaxFiles || m.OnlyA == nil || m.OnlyB == nil {
		t.Errorf("cap: %d files, more %d, added %d", len(m.Files), m.MoreFiles, m.Added)
	}
}

func TestIsBinaryDiff(t *testing.T) {
	for text, want := range map[string]bool{
		"diff --git a/x b/x\nBinary files a/x and b/x differ\n": true,
		"diff --git a/x b/x\nGIT binary patch\nliteral 3\n":     true,
		"diff --git a/x b/x\n+Binary files are fine\n":          false,
		"": false,
	} {
		if IsBinaryDiff(text) != want {
			t.Errorf("IsBinaryDiff(%q) = %v", text, !want)
		}
	}
}

func TestParseEntry(t *testing.T) {
	h := strings.Repeat("a", 40)
	for _, c := range []struct {
		gs, action, message, what string
	}{
		{"commit: fix login", "commit", "fix login", "Committed"},
		{"commit (amend): fix login", "commit", "fix login", "Amended a commit"},
		{"commit (initial): first", "commit", "first", "Made the first commit"},
		{"commit (merge): Merge branch 'x'", "commit", "Merge branch 'x'", "Made a merge commit"},
		{"checkout: moving from main to dev", "checkout", "moving from main to dev", "Switched to another branch or commit"},
		{"merge dev: Fast-forward", "merge", "Fast-forward", "Merged"},
		{"rebase (finish): returning to refs/heads/x", "rebase", "returning to refs/heads/x", "Rebased"},
		{"reset: moving to HEAD~1", "reset", "moving to HEAD~1", "Moved the branch (reset)"},
		{"pull: Fast-forward", "pull", "Fast-forward", "Pulled"},
		{"clone: from https://x/y", "clone", "from https://x/y", "Cloned"},
		{"branch: Created from HEAD", "branch", "Created from HEAD", "Created a branch"},
		{"cherry-pick: add a", "cherry-pick", "add a", "Cherry-picked"},
		{"update by push", "update", "", "Updated"},
		{"stash:", "stash", "", "Stash"},
	} {
		e, ok := ParseEntry(h + "\x1fHEAD@{12}\x1f" + c.gs + "\x1f1700000000\x1fsubject here")
		if !ok || e.Action != c.action || e.Message != c.message || e.What != c.what {
			t.Errorf("%q = %+v", c.gs, e)
		}
		if e.Index != 12 || e.Ref != "HEAD@{12}" || e.Short != "aaaaaaa" || e.Time != 1700000000 || e.Subject != "subject here" {
			t.Errorf("%q fields = %+v", c.gs, e)
		}
	}
	for _, bad := range []string{"", "x\x1fy", "short\x1fHEAD@{0}\x1fcommit: a\x1f1\x1fs"} {
		if _, ok := ParseEntry(bad); ok {
			t.Errorf("ParseEntry(%q) accepted", bad)
		}
	}
	if e, _ := ParseEntry(h + "\x1fmain@{3}\x1fcommit: a\x1f1\x1fs"); e.Index != 3 {
		t.Errorf("branch reflog index = %d", e.Index)
	}
}

func TestCheckReflogRef(t *testing.T) {
	for in, want := range map[string]string{"": "HEAD", " HEAD ": "HEAD", "main": "main", "feature/x": "feature/x"} {
		if got, err := CheckReflogRef(in); err != nil || got != want {
			t.Errorf("CheckReflogRef(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-x", "a b", "--all"} {
		if _, err := CheckReflogRef(bad); err == nil {
			t.Errorf("CheckReflogRef(%q) accepted", bad)
		}
	}
}

func TestParseLog(t *testing.T) {
	h := func(c string) string { return strings.Repeat(c, 40) }
	log := "# bad: [" + h("a") + "] c16\n# good: [" + h("b") + "] c1\ngit bisect start 'main' 'HEAD~15'\n" +
		"# skip: [" + h("c") + "] c8: a subject with: colons\ngit bisect skip " + h("c") + "\n" +
		"# only skipped commits left to test\n# possible first bad commit: [" + h("d") + "] c9\n# possible first bad commit: [" + h("e") + "] c8\n"
	steps, culprit, maybe := ParseLog(log)
	if len(steps) != 3 || steps[0].Mark != Bad || steps[1].Mark != Good || steps[2] != (Step{Mark: Skip, Hash: h("c"), Short: "ccccccc", Subject: "c8: a subject with: colons"}) {
		t.Errorf("steps = %+v", steps)
	}
	if culprit != "" || !reflect.DeepEqual(maybe, []string{h("d"), h("e")}) {
		t.Errorf("culprit %q maybe %v", culprit, maybe)
	}
	_, culprit, _ = ParseLog("# first bad commit: [" + h("f") + "] c5\r\n")
	if culprit != h("f") {
		t.Errorf("culprit = %q", culprit)
	}
	if s, c, m := ParseLog(""); s != nil || c != "" || m != nil {
		t.Error("empty log")
	}
}

func TestEstimateSteps(t *testing.T) {
	// git's own numbers: "roughly N steps" for N candidates.
	for n, want := range map[int]int{0: 0, 1: 0, 2: 0, 3: 1, 4: 1, 5: 1, 7: 2, 8: 2, 15: 3, 16: 3, 100: 6, 1000: 9} {
		if got := EstimateSteps(n); got != want {
			t.Errorf("EstimateSteps(%d) = %d, want %d", n, got, want)
		}
	}
	if StepsWords(2) != "about 2 steps" || StepsWords(1) != "about 1 step" || StepsWords(0) != "about 1 step" {
		t.Error("StepsWords")
	}
}

func TestCheckStart(t *testing.T) {
	ok := refs.Head{Branch: "main", Hash: "abc"}
	for _, c := range []struct {
		name string
		h    refs.Head
		err  string
	}{
		{"fine", ok, ""},
		{"untracked only", refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{New: 4}}, ""},
		{"detached", refs.Head{Hash: "abc"}, ""},
		{"no commits", refs.Head{Branch: "main"}, "no commits"},
		{"dirty", refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{Changed: 1}}, "commit, park or discard"},
		{"staged", refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{Staged: 2}}, "commit, park or discard"},
		{"conflicts", refs.Head{Branch: "main", Hash: "abc", Changes: refs.Changes{Conflicts: 1}}, "conflicts"},
		{"merging", refs.Head{Branch: "main", Hash: "abc", Busy: "merge"}, "middle of a merge"},
		{"rebasing", refs.Head{Hash: "abc", Busy: "rebase"}, "middle of a rebase"},
		{"running", refs.Head{Hash: "abc", Busy: "bisect"}, "already running"},
	} {
		err := CheckStart(c.h)
		if (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: %v, want %q", c.name, err, c.err)
		}
	}
	if CheckRunning(refs.Head{Busy: "bisect"}) != nil || CheckRunning(refs.Head{}) == nil || CheckRunning(refs.Head{Busy: "merge"}) == nil {
		t.Error("CheckRunning")
	}
}

func TestCheckMark(t *testing.T) {
	for in, want := range map[string]string{"good": Good, " BAD ": Bad, "skip": Skip} {
		if got, err := CheckMark(in); err != nil || got != want {
			t.Errorf("CheckMark(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := CheckMark("old"); err == nil {
		t.Error("unknown mark accepted")
	}
}

func TestStateWords(t *testing.T) {
	next := &Commit{Short: "1a2b3c4", Subject: "Add login"}
	culprit := &Culprit{Commit: Commit{Short: "9f8e7d6", Subject: "Break login", Author: "Ana"}}
	for _, c := range []struct {
		s    State
		want string
	}{
		{State{}, "No bisect is running."},
		{State{Running: true, Next: next, StepsLeft: 3}, "Test 1a2b3c4 Add login, then mark it good, bad or skip. about 3 steps left."},
		{State{Running: true, Culprit: culprit}, "The first bad commit is 9f8e7d6 Break login, by Ana."},
		{State{Running: true, Maybe: []Commit{{}, {}}}, "Too many commits were skipped to tell: the first bad commit is one of 2."},
		{State{Running: true}, "A bisect is running."},
	} {
		if got := c.s.Words(); got != c.want {
			t.Errorf("Words = %q, want %q", got, c.want)
		}
	}
}
