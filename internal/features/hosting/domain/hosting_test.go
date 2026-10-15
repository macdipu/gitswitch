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
