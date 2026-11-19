package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

func TestSigGlyphColours(t *testing.T) {
	for status, colour := range map[string]string{"G": cGreen, "B": cRed, "R": cRed, "X": cYellow, "Y": cYellow, "E": cDim} {
		if g := sigGlyph(status); !strings.HasPrefix(g, colour) || !strings.Contains(g, "✓") {
			t.Errorf("%s: %q", status, g)
		}
	}
	if sigGlyph("N") != " " {
		t.Error("an unsigned commit gets a blank")
	}
}

// signingAccount adds an account with a fresh ssh key; the test is skipped
// without ssh-keygen.
func signingAccount(t *testing.T, x *tui) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	key := filepath.Join(t.TempDir(), "id_work")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen: %v\n%s", err, out)
	}
	if _, err := x.app.Accounts.Add(account.Account{ID: "work", Host: "github.com", Name: "T", Email: "t@x.example", Key: key}, account.KeyChoice{}); err != nil {
		t.Fatal(err)
	}
	x.reload()
	return key
}

func TestSigningSetAndClear(t *testing.T) {
	x := testTUI(t)
	signingAccount(t, x)
	x.switchTab(tabAccounts)
	x.handle("s")
	if x.form == nil || x.form.fields[0].value != "Off" {
		t.Fatalf("no signing form: %+v (msg %q)", x.form, x.msg)
	}
	x.handle("right") // SSH key
	x.handle("enter")
	x.handle("enter") // the account's own key
	if x.view == nil {
		t.Fatalf("no steps shown (msg %q)", x.msg)
	}
	text := strings.Join(x.view.lines, "\n")
	if !strings.Contains(text, "ssh-ed25519") || !strings.Contains(text, "PUBLIC key") || x.view.copy == "" {
		t.Fatalf("view:\n%s", text)
	}
	if a := x.cfg.Account("work"); !a.Signs() || a.SigningFormat != account.SigningSSH {
		t.Fatalf("not saved: %+v", a)
	}
	x.handle("esc")
	lines, _ := x.drawAccounts()
	if s := history.StripANSI(strings.Join(lines, "\n")); !strings.Contains(s, "signs commits (ssh)") {
		t.Fatalf("not in the list:\n%s", s)
	}
	x.handle("s")
	x.handle("left") // Off
	x.handle("enter")
	x.handle("enter")
	if x.cfg.Account("work").Signs() || !strings.Contains(x.msg, "does not sign") {
		t.Fatalf("not cleared (msg %q)", x.msg)
	}
	// A gpg key needs its id.
	x.handle("s")
	x.handle("left") // GPG key (wraps)
	x.handle("enter")
	x.handle("enter")
	if !x.msgErr {
		t.Fatalf("gpg without a key was accepted (msg %q)", x.msg)
	}
}

// With an account that signs, the History list marks signed commits.
func TestHistoryBadges(t *testing.T) {
	x, dir, _, git := remoteRepo(t)
	signingAccount(t, x)
	if _, err := x.app.Accounts.SetSigning("work", "ssh", ""); err != nil {
		t.Fatal(err)
	}
	x.reload()
	gd := x.histDir
	if _, err := x.app.Accounts.SetRepoAccount(gd, "work"); err != nil {
		t.Fatal(err)
	}
	if git("config", "commit.gpgsign") != "true" {
		t.Skip("the repo did not get signing settings")
	}
	if err := os.WriteFile(filepath.Join(dir, "signed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "signed one")
	x.loadHistory()
	lines, _ := x.drawHistory()
	s := strings.Join(lines, "\n")
	if !strings.Contains(history.StripANSI(s), "signatures:") {
		t.Fatalf("no legend:\n%s", history.StripANSI(s))
	}
	g := sigGlyph("G")
	found := false
	for _, l := range lines {
		if strings.Contains(l, "signed one") && strings.Contains(l, g) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no green check on the signed commit:\n%q", s)
	}
	// The older commits are not signed: a blank, not a check.
	for _, l := range lines {
		if strings.Contains(l, "second") && strings.Contains(history.StripANSI(l), "✓") {
			t.Fatalf("unsigned commit marked: %q", l)
		}
	}
}

// Without an account that signs the list has no badge column.
func TestNoBadgesWithoutSigning(t *testing.T) {
	x, _, _, _ := remoteRepo(t)
	if x.signsHere() || x.sigBadge(0) != "" || x.sigLegend() != nil {
		t.Fatal("badges without signing")
	}
}
