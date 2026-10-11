package gitcli

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
		h, p := ParseRemote(in)
		if h != want[0] || p != want[1] {
			t.Errorf("ParseRemote(%q) = %q %q, want %q %q", in, h, p, want[0], want[1])
		}
	}
}

func TestIsConfigWrite(t *testing.T) {
	writes := [][]string{{"--global", "user.email", "x"}, {"--unset", "a.b"}, {"--global", "--add", "a.b", "c"}, {"--remove-section", "x"}, {"--rename-section", "a", "b"}}
	reads := [][]string{{"--get", "a.b"}, {"--global", "--get-regexp", "^x"}, {"user.email"}, {"--list"}, {"-z", "--global", "--get-regexp", "x"}}
	for _, w := range writes {
		if !IsConfigWrite(w) {
			t.Errorf("%v should be a write", w)
		}
	}
	for _, r := range reads {
		if IsConfigWrite(r) {
			t.Errorf("%v should be a read", r)
		}
	}
	if ca := ConfigArgs([]string{"--git-dir=/x", "config", "--local", "a.b", "c"}); len(ca) != 3 {
		t.Errorf("configArgs = %v", ca)
	}
	if ca := ConfigArgs([]string{"-C", "/x", "log"}); ca != nil {
		t.Errorf("non-config command detected as config: %v", ca)
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
