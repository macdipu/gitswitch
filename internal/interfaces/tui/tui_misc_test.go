package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// Y redoes what U took back, asking first; the footer offers it only while
// there is something to redo.
func TestRedoAfterUndo(t *testing.T) {
	x, _, _, git := remoteRepo(t)
	git("branch", "feature")
	if strings.Contains(x.footer(), "Y redo") {
		t.Fatal("redo offered with nothing undone")
	}
	x.handle("b")
	typeKeys(x, "feature")
	x.handle("enter")
	if git("branch", "--show-current") != "feature" {
		t.Fatalf("not switched (msg %q)", x.msg)
	}
	x.handle("U")
	x.handle("y")
	if git("branch", "--show-current") != "main" {
		t.Fatalf("not undone (msg %q)", x.msg)
	}
	if !strings.Contains(x.footer(), "Y redo") {
		t.Fatalf("footer: %q", x.footer())
	}
	x.handle("Y")
	if x.confirm == nil || !strings.Contains(x.confirm.text, "Redo “") {
		t.Fatalf("Y did not ask: %+v (msg %q)", x.confirm, x.msg)
	}
	x.handle("n")
	if git("branch", "--show-current") != "main" {
		t.Fatal("n redid it")
	}
	x.handle("Y")
	x.handle("y")
	if git("branch", "--show-current") != "feature" || !strings.HasPrefix(x.msg, "Redone: ") {
		t.Fatalf("not redone: %q (msg %q)", git("branch", "--show-current"), x.msg)
	}
	if strings.Contains(x.footer(), "Y redo") {
		t.Fatal("redo still offered")
	}
	x.handle("Y")
	if !x.msgErr || !strings.Contains(x.msg, "nothing to redo") {
		t.Fatalf("msg %q", x.msg)
	}
}

func TestGitBanner(t *testing.T) {
	x := testTUI(t)
	if x.banner() != "" {
		t.Fatal("banner without a problem")
	}
	x.gitBanner() // git is installed where tests run
	if x.ext.banner != "" {
		t.Fatalf("banner: %q", x.ext.banner)
	}
	x.ext.banner = "git is not installed or not on your PATH."
	if !strings.Contains(history.StripANSI(x.banner()), "git is not installed") {
		t.Fatalf("banner: %q", x.banner())
	}
}

func TestInitFromRepos(t *testing.T) {
	x := testTUI(t)
	x.switchTab(tabRepos)
	dir := filepath.Join(t.TempDir(), "fresh")
	x.handle("n")
	if x.form == nil || len(x.form.fields) != 3 || x.form.fields[2].value != "By rules" {
		t.Fatalf("no init form: %+v (msg %q)", x.form, x.msg)
	}
	typeKeys(x, dir)
	x.handle("enter")
	typeKeys(x, "trunk")
	x.handle("enter")
	x.handle("enter")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil || x.msgErr {
		t.Fatalf("no repo: %v (msg %q)", err, x.msg)
	}
	if h, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); !strings.Contains(string(h), "trunk") {
		t.Fatalf("HEAD = %q", h)
	}
	if i := x.sel[tabRepos]; i >= len(x.repos) || x.repos[i].Path != dir {
		t.Fatalf("new repo not selected: sel %d of %d", i, len(x.repos))
	}
	// A folder that is a repo already is refused.
	x.handle("n")
	typeKeys(x, dir)
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if !x.msgErr || !strings.Contains(x.msg, "already") {
		t.Fatalf("msg %q", x.msg)
	}
}

// Several accounts and nothing deciding: the refusal turns into a list.
func TestInitAsksWhichAccount(t *testing.T) {
	x := testTUI(t)
	for _, id := range []string{"work", "home"} {
		key := filepath.Join(t.TempDir(), "id_"+id)
		if err := os.WriteFile(key, []byte("not a real key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := x.app.Accounts.Add(account.Account{ID: id, Host: "github.com", Name: "T", Email: id + "@x.example", Key: key}, account.KeyChoice{}); err != nil {
			t.Fatal(err)
		}
	}
	x.reload()
	x.switchTab(tabRepos)
	dir := filepath.Join(t.TempDir(), "two")
	x.handle("n")
	typeKeys(x, dir)
	x.handle("enter")
	x.handle("enter")
	x.handle("enter")
	if x.picker == nil || len(x.picker.options) != 2 {
		t.Fatalf("no account list: %+v (msg %q)", x.picker, x.msg)
	}
	typeKeys(x, "home")
	x.handle("enter")
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("no repo: %v (msg %q)", err, x.msg)
	}
	if got := x.app.Repos.Info(x.repos[x.sel[tabRepos]].GitDir).Account; got != "home" {
		t.Fatalf("account = %q", got)
	}
}
