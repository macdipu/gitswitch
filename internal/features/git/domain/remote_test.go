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
	for _, u := range []string{"git@github.com:acme/api.git", "ssh://git@github.com/acme/api", "https://github.com/acme/api", "https://gitlab.example.com/group/sub/repo.git"} {
		if err := CheckCloneURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "-uploadpack=x", "ext::sh -c id", "file:///tmp/repo", "/tmp/repo", "github.com", "https://github.com/", "git@github.com:"} {
		if CheckCloneURL(u) == nil {
			t.Errorf("%q should be refused", u)
		}
	}
	if n := RepoName("git@github.com:acme/api.git"); n != "api" {
		t.Errorf("RepoName = %q", n)
	}
}
