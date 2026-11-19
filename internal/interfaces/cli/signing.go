package cli

import (
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

// signingNote is the end of an `account list` line: how the account signs.
func signingNote(a account.Account) string {
	if !a.Signs() {
		return ""
	}
	return "  [signs commits: " + a.SigningFormat + "]"
}

// setSigning is `account edit <id> --signing-key K --signing-format F`.
func (x *CLI) setSigning(id, format, key string) error {
	r, err := x.app.Accounts.SetSigning(id, format, key)
	if err != nil {
		return err
	}
	a := r.Account
	if !a.Signs() {
		x.sayf("%s does not sign commits any more. Repos drop the signing settings gitswitch wrote the next time they use %s (`gitswitch use %s`).\n", a.ID, a.ID, a.ID)
		return nil
	}
	x.sayf("%s signs commits with %s: %s\n", a.ID, a.SigningFormat, a.EffectiveSigningKey())
	if r.Warning != "" {
		x.say("Warning:", r.Warning)
	}
	x.sayf("Repos start signing the next time they use %s (`gitswitch use %s`, or any pull and push).\n", a.ID, a.ID)
	x.sayf("\nImportant: add the PUBLIC key to %s as a signing key, or commits show as unverified there.\n", a.Host)
	if r.Public != "" {
		x.sayf("\n%s\n\n", r.Public)
	}
	for i, s := range r.Steps {
		x.sayf("  %d. %s\n", i+1, s)
	}
	if r.Page != "" {
		x.say("  " + r.Page)
	}
	x.say("The private key is never shown or sent anywhere.")
	return nil
}
