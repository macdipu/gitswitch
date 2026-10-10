package sshhook

import (
	"testing"
)

func TestParseSSHArgs(t *testing.T) {
	cases := []struct {
		args       []string
		host, path string
		probe      bool
	}{
		{[]string{"git@github.com", "git-upload-pack 'acme/api.git'"}, "github.com", "acme/api.git", false},
		{[]string{"-o", "SendEnv=GIT_PROTOCOL", "git@github.com", "git-receive-pack 'acme/api.git'"}, "github.com", "acme/api.git", false},
		{[]string{"-p", "443", "git@ssh.github.com", "git-upload-pack '/acme/api.git'"}, "ssh.github.com", "acme/api.git", false},
		{[]string{"-G", "-o", "SendEnv=GIT_PROTOCOL", "git@github.com"}, "github.com", "", true},
		{[]string{"git@gitlab.com", "git-upload-pack 'group/sub/proj.git'"}, "gitlab.com", "group/sub/proj.git", false},
		{[]string{"git@h.example", "git-upload-pack '~/it'\\''s.git'"}, "h.example", "it's.git", false},
		{[]string{"-4", "-oStrictHostKeyChecking=no", "user@Host.Example", "git-upload-pack 'a/b'"}, "host.example", "a/b", false},
	}
	for _, c := range cases {
		inv := Parse(c.args)
		if inv.Host != c.host || inv.RepoPath != c.path || inv.Probe != c.probe {
			t.Errorf("%v -> host %q path %q probe %v", c.args, inv.Host, inv.RepoPath, inv.Probe)
		}
	}
}
