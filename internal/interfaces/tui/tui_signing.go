package tui

import (
	"fmt"

	accountapp "github.com/macdipu/gitswitch/internal/features/account/application"
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Commit signing: s on Accounts sets how an account signs, and the History
// list marks signed commits once an account signs.

var signChoices = []string{"Off", "SSH key", "GPG key"}

func signChoiceOf(a *account.Account) string {
	switch a.SigningFormat {
	case account.SigningSSH:
		return signChoices[1]
	case account.SigningGPG:
		return signChoices[2]
	}
	return signChoices[0]
}

// signNote is the end of an account's line: how it signs.
func signNote(a account.Account) string {
	if !a.Signs() {
		return ""
	}
	return cGreen + "  · signs commits (" + a.SigningFormat + ")" + cReset
}

func (t *tui) signingForm(a *account.Account) {
	if a == nil {
		return
	}
	id := a.ID
	t.form = &form{title: "Sign commits made as " + id, fields: []field{
		{label: "Sign commits with", value: signChoiceOf(a), options: signChoices, hint: "←→ off, an ssh key or a gpg key"},
		{label: "Key", value: a.SigningKey, hint: "ssh: public key file (empty: " + id + "'s own key) · gpg: key id"},
	}, submit: func(v []string) error {
		format := map[string]string{signChoices[0]: "off", signChoices[1]: "ssh", signChoices[2]: "gpg"}[v[0]]
		r, err := t.app.Accounts.SetSigning(id, format, v[1])
		if err != nil {
			return err
		}
		t.reload()
		t.showSigning(r)
		return nil
	}}
}

func (t *tui) showSigning(r accountapp.SigningSet) {
	a := r.Account
	if !a.Signs() {
		t.say(nil, a.ID+" does not sign commits any more. Repos drop the settings GitSwitch wrote the next time they use "+a.ID+".")
		return
	}
	lines := []string{a.ID + " signs commits with " + a.SigningFormat + ": " + a.EffectiveSigningKey(), ""}
	if r.Warning != "" {
		lines = append(lines, "Warning: "+r.Warning, "")
	}
	lines = append(lines, "Repos start signing the next time they use "+a.ID+" (gitswitch use "+a.ID+", or any pull and push).", "",
		"Important: add the PUBLIC key to "+a.Host+" as a signing key, or commits show as unverified there.")
	if r.Public != "" {
		lines = append(lines, "", r.Public, "")
		if process.CopyToClipboard(r.Public) {
			lines = append(lines, "Copied to clipboard (c to copy again).", "")
		} else {
			lines = append(lines, "c  copy to clipboard", "")
		}
	}
	for i, s := range r.Steps {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, s))
	}
	if r.Page != "" {
		lines = append(lines, "  "+r.Page, "", "o  open that page")
	}
	lines = append(lines, "", "The private key is never shown or sent anywhere.")
	t.view = &viewer{title: "Signing: " + a.ID, lines: lines, url: r.Page, copy: r.Public}
	t.msg, t.msgErr = "", false
}

// ---------------- badges ----------------

// signsHere says whether the History repo's account signs its commits.
func (t *tui) signsHere() bool {
	if t.hist == nil || t.hist.Repo.Account == "" {
		return false
	}
	a := t.cfg.Account(t.hist.Repo.Account)
	return a != nil && a.Signs()
}

// loadSigs asks git about the signatures of the commit lines in view (and
// the ones around the selection), once per commit.
func (t *tui) loadSigs() {
	if !t.signsHere() || len(t.histRows) == 0 {
		return
	}
	sel := t.sel[tabHistory]
	lo := max(0, min(t.off[tabHistory], sel))
	hi := min(len(t.histRows), max(t.off[tabHistory], sel)+t.h)
	var ask []string
	for _, r := range t.histRows[lo:hi] {
		if r.Hash == "" || t.ext.tried[r.Hash] {
			continue
		}
		ask = append(ask, r.Hash)
	}
	if len(ask) == 0 {
		return
	}
	if t.ext.tried == nil {
		t.ext.tried, t.ext.sigs = map[string]bool{}, map[string]history.Signature{}
	}
	for _, h := range ask {
		t.ext.tried[h] = true
	}
	got, err := t.app.History.Signatures(t.histDir, ask)
	if err != nil {
		return
	}
	for h, s := range got {
		t.ext.sigs[h] = s
	}
}

// sigGlyph is the badge for a git signature status letter.
func sigGlyph(status string) string {
	switch status {
	case "G":
		return cGreen + "✓" + cReset
	case "B", "R":
		return cRed + "✓" + cReset
	case "U", "X", "Y":
		return cYellow + "✓" + cReset
	case "E":
		return cDim + "✓" + cReset
	}
	return " "
}

// sigBadge is the narrow column before a history line: one character when
// the repo's account signs, nothing otherwise.
func (t *tui) sigBadge(i int) string {
	if !t.signsHere() {
		return ""
	}
	if i < len(t.histRows) {
		if s, ok := t.ext.sigs[t.histRows[i].Hash]; ok {
			return sigGlyph(s.Status)
		}
	}
	return " "
}

// sigLegend is the History header line explaining the badges.
func (t *tui) sigLegend() []string {
	if !t.signsHere() {
		return nil
	}
	return []string{"\x00 " + cDim + "signatures: " + cReset + sigGlyph("G") + cDim + " good  " + cReset + sigGlyph("B") + cDim + " bad or revoked  " + cReset + sigGlyph("X") + cDim + " expired  " + cReset + sigGlyph("E") + cDim + " cannot check here" + cReset}
}
