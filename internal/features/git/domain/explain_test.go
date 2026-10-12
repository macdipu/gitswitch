package domain

import (
	"strings"
	"testing"
)

func TestExplain(t *testing.T) {
	cases := []struct{ msg, want, action string }{
		{"git@github.com: Permission denied (publickey).\r\nfatal: Could not read from remote repository.", "does not recognise this account's key", "pubkey"},
		{"Host key verification failed.\nfatal: Could not read from remote repository.", "not confirmed this server's identity", "test"},
		{"@@@@@\n@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\nHost key verification failed.", "identity is different", ""},
		{"ERROR: Repository not found.\nfatal: Could not read from remote repository.", "found no repo", ""},
		{"ERROR: Permission to acme/api.git denied to dipu-personal.\nfatal: unable to access", "signed in as dipu-personal, which cannot push to acme/api.git", ""},
		{"ssh: Could not resolve hostname githb.com: nodename nor servname provided, or not known", "could not be found", ""},
		{"ssh: connect to host github.com port 22: Connection timed out", "connection to the host failed", ""},
		{"Author identity unknown\n\n*** Please tell me who you are.", "no account yet", ""},
		{"error: could not lock config file /home/u/.gitconfig: File exists", "same moment", ""},
		{"error: Your local changes to the following files would be overwritten by checkout:\n\ta.txt", "not committed yet", ""},
		{" ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs", "push was refused", ""},
		{"fatal: Not possible to fast-forward, aborting.", "both have new commits", ""},
		{"Permissions 0644 for '/home/u/.ssh/k' are too open.\nIt is required that your private key files are NOT accessible by others.\nLoad key \"/home/u/.ssh/k\": bad permissions", "can read the key file", ""},
	}
	for _, c := range cases {
		h := Explain(c.msg)
		if h == nil {
			t.Errorf("no hint for %q", c.msg)
			continue
		}
		if !strings.Contains(h.Problem, c.want) || h.Action != c.action || h.Fix == "" {
			t.Errorf("%q:\n got %+v\nwant problem containing %q, action %q", c.msg, h, c.want, c.action)
		}
	}
	for _, msg := range []string{"", "  ", "fatal: something nobody has seen before"} {
		if h := Explain(msg); h != nil {
			t.Errorf("%q: want no hint, got %+v", msg, h)
		}
	}
}
