package domain

import (
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := map[string][2]string{
		"git@github.com:acme/api.git":           {"github.com", "acme/api.git"},
		"ssh://git@ssh.github.com:443/acme/api": {"ssh.github.com", "acme/api"},
		"https://github.com/acme/api.git":       {"github.com", "acme/api.git"},
		"https://user@GitHub.com/acme/api":      {"github.com", "acme/api"},
		"alias-host:acme/api.git":               {"alias-host", "acme/api.git"},
		"git@[::1]:acme/api.git":                {"::1", "acme/api.git"},
		"[git@Host.example:2222]:acme/api":      {"host.example", "acme/api"},
		"[alias]:acme/api":                      {"alias", "acme/api"},
		`C:\src\repo`:                           {"", ""},
		"c:/src/repo":                           {"", ""},
		"/local/path/repo.git":                  {"", ""},
		"":                                      {"", ""},
	}
	for in, want := range cases {
		h, p := Parse(in)
		if h != want[0] || p != want[1] {
			t.Errorf("Parse(%q) = %q %q, want %q %q", in, h, p, want[0], want[1])
		}
	}
}

func TestCanonicalHost(t *testing.T) {
	if !SameHost("ssh.github.com", "GitHub.com") || !SameHost("altssh.gitlab.com", "gitlab.com") || SameHost("github.com", "gitlab.com") {
		t.Fatal("canonical host mapping wrong")
	}
}

func TestCheckCloneURL(t *testing.T) {
	for _, u := range []string{"git@github.com:acme/api.git", "ssh://git@github.com/acme/api", "https://github.com/acme/api", "https://gitlab.example.com/group/sub/repo.git",
		"git@[::1]:acme/api.git", "[git@example.com:2222]:acme/api.git"} {
		if err := CheckCloneURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "-uploadpack=x", "ext::sh -c id", "file:///tmp/repo", "/tmp/repo", "github.com", "https://github.com/", "git@github.com:",
		`C:\src\repo`, "c:/src/repo", "c:src/repo", `D:\x`, "x:owner/repo", "[git@github.com:acme/api", "fd::17", "ext::git@github.com:a/b"} {
		if CheckCloneURL(u) == nil {
			t.Errorf("%q should be refused", u)
		}
	}
	if n := RepoName("git@github.com:acme/api.git"); n != "api" {
		t.Errorf("RepoName = %q", n)
	}
}

func TestWebAndCommitURL(t *testing.T) {
	cases := []struct{ remote, web, commit string }{
		{"git@github.com:acme/api.git", "https://github.com/acme/api", "https://github.com/acme/api/commit/abc"},
		{"ssh://git@ssh.github.com:443/acme/api.git", "https://github.com/acme/api", "https://github.com/acme/api/commit/abc"},
		{"https://user@gitlab.com/group/sub/app.git", "https://gitlab.com/group/sub/app", "https://gitlab.com/group/sub/app/-/commit/abc"},
		{"git@bitbucket.org:team/site.git", "https://bitbucket.org/team/site", "https://bitbucket.org/team/site/commits/abc"},
		{"/srv/git/local.git", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := WebURL(c.remote); got != c.web {
			t.Errorf("WebURL(%q) = %q, want %q", c.remote, got, c.web)
		}
		if got := CommitURL(c.remote, "abc"); got != c.commit {
			t.Errorf("CommitURL(%q) = %q, want %q", c.remote, got, c.commit)
		}
	}
}

func TestPullRequestURL(t *testing.T) {
	for _, c := range []struct{ remote, branch, want string }{
		{"git@github.com:acme/api.git", "feature/x", "https://github.com/acme/api/pull/new/feature/x"},
		{"https://github.com/acme/api", "fix#1", "https://github.com/acme/api/pull/new/fix%231"},
		{"git@gitlab.com:acme/sub/api.git", "feature/x", "https://gitlab.com/acme/sub/api/-/merge_requests/new?merge_request%5Bsource_branch%5D=feature%2Fx"},
		{"git@bitbucket.org:acme/api.git", "fix", "https://bitbucket.org/acme/api/pull-requests/new?source=fix"},
		{"git@git.example.com:acme/api.git", "fix", ""},
		{"/srv/git/api.git", "fix", ""},
		{"git@github.com:acme/api.git", "", ""},
	} {
		if got := PullRequestURL(c.remote, c.branch); got != c.want {
			t.Errorf("PullRequestURL(%q, %q) = %q, want %q", c.remote, c.branch, got, c.want)
		}
	}
}
