package infrastructure

import (
	"testing"
)

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
