package domain

import (
	"strings"
	"testing"
)

func TestCheckRemoteName(t *testing.T) {
	for _, ok := range []string{"origin", "upstream", "my-fork", "a.b", "r_2"} {
		if err := CheckRemoteName(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", ".x", "a b", "a/b", "a..b", "x.", "x.lock", "a:b", "é"} {
		if err := CheckRemoteName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSplitRemoteBranch(t *testing.T) {
	remotes := []string{"origin", "origin/x", "up"}
	for ref, want := range map[string][3]string{
		"origin/feature/x": {"origin", "feature/x", "true"},
		"origin/x/y":       {"origin/x", "y", "true"}, // the longest remote name wins
		"up/main":          {"up", "main", "true"},
		"origin":           {"", "", "false"},
		"origin/":          {"", "", "false"},
		"other/main":       {"", "", "false"},
	} {
		r, b, ok := SplitRemoteBranch(ref, remotes)
		if got := [3]string{r, b, map[bool]string{true: "true", false: "false"}[ok]}; got != want {
			t.Errorf("%s = %v, want %v", ref, got, want)
		}
	}
}

func TestPruneSetting(t *testing.T) {
	for _, c := range []struct {
		global, repo string
		want         PruneSetting
	}{
		{"", "", PruneSetting{}},
		{"true", "", PruneSetting{On: true, Global: "on"}},
		{"true", "false", PruneSetting{Global: "on", Repo: "off"}},
		{"false", "true", PruneSetting{On: true, Global: "off", Repo: "on"}},
		{"nonsense", "", PruneSetting{}},
	} {
		if got := NewPruneSetting(c.global, c.repo); got != c.want {
			t.Errorf("%q %q = %+v, want %+v", c.global, c.repo, got, c.want)
		}
	}
}

func TestDetachedWords(t *testing.T) {
	w := DetachedWords("abc1234", "main")
	if !strings.Contains(w, "abc1234") || !strings.Contains(w, "not on a branch") || !strings.Contains(w, "go back to main") {
		t.Errorf("words: %s", w)
	}
	if strings.Contains(DetachedWords("abc1234", ""), "go back") {
		t.Error("no way back is offered without a branch")
	}
}
