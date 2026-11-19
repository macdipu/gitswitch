package application

import (
	"errors"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
)

// SigningSet is an account after its signing was set, and what to do on
// its host: the PUBLIC key has to be added there as a signing key before
// the commits show as verified.
type SigningSet struct {
	Account account.Account `json:"account"`
	Public  string          `json:"public"` // the public key to add (ssh); "" for gpg or off
	Page    string          `json:"page"`   // the host's page for it, when known
	Steps   []string        `json:"steps"`
	Warning string          `json:"warning"` // e.g. the key file cannot be read yet
}

// SetSigning sets or clears how an account signs its commits. format is
// ssh, gpg or off ("" leaves it, or guesses it from key when the account
// does not sign yet); key is the ssh public key file (or key::<key>) or the
// gpg key id ("" leaves it; with ssh and none, the account's own key is
// used). Repos pick the change up the next time they use the account
// (`gitswitch use`, or any pull and push).
func (s *Service) SetSigning(id, format, key string) (SigningSet, error) {
	acc, err := s.Get(id)
	if err != nil {
		return SigningSet{}, err
	}
	if format == "" && key == "" {
		return SigningSet{}, errors.New("give a signing key, a format (ssh, gpg or off), or both")
	}
	upd := *acc
	if format != "" {
		f, err := account.ParseSigningFormat(format)
		if err != nil {
			return SigningSet{}, err
		}
		if f != upd.SigningFormat && key == "" {
			upd.SigningKey = "" // a key of the other kind
		}
		upd.SigningFormat = f
		if f == account.SigningOff {
			upd.SigningKey = ""
		}
	}
	if key != "" {
		upd.SigningKey = key
		if format == "" && upd.SigningFormat == account.SigningOff {
			upd.SigningFormat = account.InferSigningFormat(key)
		}
	}
	if err := upd.ValidateSigning(); err != nil {
		return SigningSet{}, err
	}
	if err := s.Store.Save(upd); err != nil { // not Update: that keeps the old signing
		return SigningSet{}, err
	}
	out := SigningSet{Account: upd}
	if !upd.Signs() {
		return out, nil
	}
	out.Page, out.Steps = account.SigningHelp(upd.Host, upd.SigningFormat)
	if sk, ok := s.Store.(account.SigningKeys); ok && upd.SigningFormat == account.SigningSSH {
		pub, err := sk.PublicSigningKey(upd)
		if err != nil {
			out.Warning = err.Error()
		}
		out.Public = pub
	}
	return out, nil
}
