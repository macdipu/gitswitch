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
		"gitlab.com":           "",
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
