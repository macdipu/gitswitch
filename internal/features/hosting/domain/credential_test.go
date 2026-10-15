package domain

import (
	"reflect"
	"testing"
)

func TestHelperCommand(t *testing.T) {
	if got, ok := HelperCommand(`"/home/me/.local/bin/gitswitch" ssh`); !ok || got != `!"/home/me/.local/bin/gitswitch" credential` {
		t.Errorf("HelperCommand = %q, %v", got, ok)
	}
	for _, h := range []string{"", "ssh -i ~/.ssh/id", `"/usr/bin/gitswitch" ssh -v`} {
		if _, ok := HelperCommand(h); ok {
			t.Errorf("HelperCommand(%q) must not be gitswitch's", h)
		}
	}
	if s := HelperSection("SSH.GitHub.com"); s != "credential.https://github.com/" {
		t.Errorf("HelperSection = %q", s)
	}
}

func TestPreviousHelpers(t *testing.T) {
	entries := []HelperEntry{
		{"", "cache"},
		{"", ""}, // starts the list again
		{"", "osxkeychain"},
		{"https://github.com", "manager"},
		{"github.com", "store"},
		{"https://gist.github.com", "other-host"},
		{"https://github.com/acme", "one-path"},
		{"http://github.com", "plain-http"},
		{"https://me@github.com/", "with-user"},
		{"https://github.com/", "!\"/old/gitswitch\" credential"}, // GitSwitch's own section
	}
	want := []string{"osxkeychain", "manager", "store", "with-user"}
	if got := PreviousHelpers(entries, "github.com"); !reflect.DeepEqual(got, want) {
		t.Errorf("PreviousHelpers = %q, want %q", got, want)
	}
	if got := PreviousHelpers([]HelperEntry{{"https://github.com", ""}}, "github.com"); got != nil {
		t.Errorf("only a reset: %q", got)
	}
}

func TestParseRequest(t *testing.T) {
	r := ParseRequest("protocol=https\r\nhost=github.com\nusername=dipu\npath=acme/api.git\n\nhost=ignored\n")
	if r != (Request{Protocol: "https", Host: "github.com", Path: "acme/api.git", Username: "dipu"}) {
		t.Errorf("ParseRequest = %+v", r)
	}
	if TokenUser("") != "x-access-token" || TokenUser("dipu") != "dipu" {
		t.Error("TokenUser")
	}
}
