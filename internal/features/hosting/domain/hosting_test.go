package domain

import (
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

func TestAPIBase(t *testing.T) {
	cases := map[string]string{
		"github.com":           "https://api.github.com",
		"SSH.GitHub.com":       "https://api.github.com",
		"github.acme.corp":     "https://github.acme.corp/api/v3",
		"gitlab.com":           "https://gitlab.com/api/v4",
		"altssh.gitlab.com":    "https://gitlab.com/api/v4",
		"gitlab.acme.corp":     "https://gitlab.acme.corp/api/v4",
		"bitbucket.org":        "",
		"altssh.bitbucket.org": "",
		"":                     "",
	}
	for host, want := range cases {
		got, err := APIBase(host)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("APIBase(%q) = %q, %v; want %q", host, got, err, want)
		}
	}
}

func TestCheckToken(t *testing.T) {
	if got, err := CheckToken("  github_pat_11AB_cd \n"); err != nil || got != "github_pat_11AB_cd" {
		t.Errorf("CheckToken = %q, %v", got, err)
	}
	for _, bad := range []string{"", "  ", "two words", `quo"te`, "it's", `back\slash`, "tab\there", "ünï", strings.Repeat("a", 256)} {
		if _, err := CheckToken(bad); err == nil {
			t.Errorf("CheckToken(%q) must fail", bad)
		}
	}
}

func TestSameUser(t *testing.T) {
	a := &account.Account{ID: "work", Host: "github.com", User: "Dipu-Work"}
	if err := SameUser(a, "dipu-work"); err != nil {
		t.Errorf("the same user in another case: %v", err)
	}
	if err := SameUser(a, "someone"); err == nil || !strings.Contains(err.Error(), "logs in as Dipu-Work") {
		t.Errorf("another user: %v", err)
	}
	if err := SameUser(&account.Account{ID: "new"}, "anyone"); err != nil {
		t.Errorf("an untested account takes any user: %v", err)
	}
}

func TestStatusChips(t *testing.T) {
	kinds := func(p PullRequest) string {
		var out []string
		for _, c := range StatusChips(p) {
			out = append(out, c.Kind)
		}
		return strings.Join(out, " ")
	}
	cases := []struct {
		pr   PullRequest
		want string
	}{
		{PullRequest{ReviewDecision: "APPROVED", Checks: "SUCCESS", Mergeable: "MERGEABLE"}, "ready"},
		{PullRequest{ReviewDecision: "APPROVED"}, "ready"},
		{PullRequest{ReviewDecision: "APPROVED", Checks: "FAILURE"}, "failing approved"},
		{PullRequest{ReviewDecision: "APPROVED", Checks: "PENDING"}, "running approved"},
		{PullRequest{ReviewDecision: "CHANGES_REQUESTED", Mergeable: "CONFLICTING"}, "conflict changes"},
		{PullRequest{ReviewDecision: "REVIEW_REQUIRED"}, "waiting"},
		{PullRequest{ReviewDecision: "REVIEW_REQUIRED", Draft: true}, "draft"},
		{PullRequest{Checks: "ERROR"}, "failing"},
		{PullRequest{}, ""},
	}
	for _, c := range cases {
		if got := kinds(c.pr); got != c.want {
			t.Errorf("%+v: %q, want %q", c.pr, got, c.want)
		}
	}
	for _, c := range StatusChips(PullRequest{Mergeable: "CONFLICTING", ReviewDecision: "CHANGES_REQUESTED", Checks: "FAILURE"}) {
		if !c.Bad || c.Hint == "" {
			t.Errorf("what needs you says what to do: %+v", c)
		}
	}
}

func TestInboxQueriesAndGraphQLURL(t *testing.T) {
	r, m, rv := InboxQueries("dipu")
	if !strings.Contains(r, "review-requested:dipu") || !strings.Contains(m, "author:dipu") || !strings.Contains(rv, "reviewed-by:dipu -author:dipu") || !strings.Contains(r, "is:open") {
		t.Errorf("queries: %q %q %q", r, m, rv)
	}
	for base, want := range map[string]string{
		"https://api.github.com":           "https://api.github.com/graphql",
		"https://github.acme.corp/api/v3":  "https://github.acme.corp/api/graphql",
		"https://github.acme.corp/api/v3/": "https://github.acme.corp/api/graphql",
		"http://127.0.0.1:5555":            "http://127.0.0.1:5555/graphql",
	} {
		if got := GraphQLURL(base); got != want {
			t.Errorf("GraphQLURL(%q) = %q", base, got)
		}
	}
}

func TestBranchSummaryAndRepoOf(t *testing.T) {
	for _, c := range []struct {
		pr   PullRequest
		want string
	}{
		{PullRequest{Number: 42, Approvals: 1, Checks: "SUCCESS"}, "PR #42 · 1 approval · checks passing"},
		{PullRequest{Number: 5, Approvals: 2, Checks: "FAILURE", Mergeable: "CONFLICTING"}, "PR #5 · 2 approvals · checks failing · conflicts"},
		{PullRequest{Number: 6, Draft: true, ReviewDecision: "REVIEW_REQUIRED", Checks: "PENDING"}, "PR #6 · draft · waiting for review · checks running"},
		{PullRequest{Number: 7, ReviewDecision: "CHANGES_REQUESTED", Approvals: 1}, "PR #7 · changes requested"},
		{PullRequest{Number: 8}, "PR #8 · no approvals"},
	} {
		if got := BranchSummary(c.pr); got != c.want {
			t.Errorf("BranchSummary = %q, want %q", got, c.want)
		}
	}
	for in, want := range map[string]string{
		"git@github.com:acme/api.git":      "acme/api",
		"https://github.com/acme/api":      "acme/api",
		"https://github.com/acme/api.git/": "acme/api",
		"https://gitlab.com/a/b/c.git":     "",
		"/home/me/repo":                    "",
		"":                                 "",
	} {
		if got := RepoOf(in); got != want {
			t.Errorf("RepoOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindOfAndWords(t *testing.T) {
	for host, want := range map[string]Kind{"github.com": KindGitHub, "ssh.github.com": KindGitHub, "github.acme.corp": KindGitHub, "gitlab.com": KindGitLab,
		"altssh.gitlab.com": KindGitLab, "gitlab.acme.corp": KindGitLab, "git.acme.corp": KindGitHub, "": KindGitHub} {
		if got := KindOf(host); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", host, got, want)
		}
	}
	if KindGitLab.Name() != "GitLab" || KindGitLab.RequestName() != "merge request" || KindGitLab.Short() != "MR" || KindGitLab.Mark() != "!" ||
		KindGitHub.Name() != "GitHub" || KindGitHub.RequestName() != "pull request" || KindGitHub.Short() != "PR" || KindGitHub.Mark() != "#" {
		t.Error("the words for each kind of host")
	}
	page, needs := TokenHelp("gitlab.com")
	if page != "https://gitlab.com/-/user_settings/personal_access_tokens" || !strings.Contains(needs, "read_api") {
		t.Errorf("GitLab token help: %q %q", page, needs)
	}
	if page, needs := TokenHelp("ssh.github.com"); page != "https://github.com/settings/personal-access-tokens/new" || !strings.Contains(needs, "Pull requests") {
		t.Errorf("GitHub token help: %q %q", page, needs)
	}
}

func TestRepoPathAndSummaryOnGitLab(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.com:acme/api.git":          "acme/api",
		"https://gitlab.com/acme/team/api.git": "acme/team/api",
		"https://gitlab.com/solo":              "",
	} {
		if got := RepoPath(KindGitLab, in); got != want {
			t.Errorf("RepoPath(gitlab, %q) = %q, want %q", in, got, want)
		}
	}
	if RepoPath(KindGitHub, "https://github.com/acme/team/api") != "" {
		t.Error("GitHub has only owner/name")
	}
	p := PullRequest{Kind: KindGitLab, Number: 7, Approvals: 2, Checks: "SUCCESS"}
	if got := BranchSummary(p); got != "MR !7 · 2 approvals · checks passing" {
		t.Errorf("BranchSummary = %q", got)
	}
	chips := StatusChips(PullRequest{Kind: KindGitLab, ReadyToMerge: true, Mergeable: "MERGEABLE", Checks: "SUCCESS"})
	if len(chips) != 1 || chips[0].Kind != "ready" || chips[0].Text != "Ready to merge" {
		t.Errorf("ready to merge: %+v", chips)
	}
	for _, c := range StatusChips(PullRequest{ReadyToMerge: true, ReviewDecision: "CHANGES_REQUESTED"}) {
		if c.Kind == "ready" {
			t.Error("changes requested is never ready")
		}
	}
}
