// Package application signs accounts in to their host's API with a token
// kept in the OS keychain, never in git config. It depends only on domain
// ports. Import it as hostingapp.
package application

import (
	"errors"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// Service runs the hosting use cases.
type Service struct {
	Accounts hosting.Accounts
	Secrets  hosting.Secrets
	API      hosting.API
}

// SignIn checks a token with the host and saves it for the account. A
// token for another user than the one the account's key logs in as is
// refused; when that user is not known yet, it is learned here.
func (s *Service) SignIn(accountID, token string) (hosting.SignedIn, error) {
	a, err := s.Accounts.Load().Lookup(accountID)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	base, err := hosting.APIBase(a.Host)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	if token, err = hosting.CheckToken(token); err != nil {
		return hosting.SignedIn{}, err
	}
	u, g, err := s.API.Whoami(base, token)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	if err := hosting.SameUser(a, u.Login); err != nil {
		return hosting.SignedIn{}, err
	}
	if err := s.Secrets.Set(a.ID, token); err != nil {
		return hosting.SignedIn{}, err
	}
	if a.User == "" {
		_ = s.Accounts.SetUser(a.ID, u.Login) // only a hint for later checks
	}
	return hosting.SignedIn{Account: a.ID, Host: a.Host, User: u, Grant: g}, nil
}

// SignOut forgets an account's token. Signing out twice is fine.
func (s *Service) SignOut(accountID string) error {
	a, err := s.Accounts.Load().Lookup(accountID)
	if err != nil {
		return err
	}
	return s.Secrets.Delete(a.ID)
}

// Status says which accounts are signed in, without the network.
func (s *Service) Status() []hosting.Status {
	out := []hosting.Status{}
	for _, a := range s.Accounts.Load().Accounts {
		st := hosting.Status{Account: a.ID, Host: a.Host}
		if _, err := hosting.APIBase(a.Host); err == nil {
			st.Supported = true
			switch _, err := s.Secrets.Get(a.ID); {
			case err == nil:
				st.SignedIn = true
			case !errors.Is(err, hosting.ErrNoToken):
				st.Error = err.Error()
			}
		}
		out = append(out, st)
	}
	return out
}

// Check asks the host whether an account's saved token still works.
func (s *Service) Check(accountID string) (hosting.SignedIn, error) {
	a, err := s.Accounts.Load().Lookup(accountID)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	base, err := hosting.APIBase(a.Host)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	token, err := s.Secrets.Get(a.ID)
	if errors.Is(err, hosting.ErrNoToken) {
		return hosting.SignedIn{}, errors.New(a.ID + " is not signed in to " + a.Host + ": gitswitch signin " + a.ID)
	} else if err != nil {
		return hosting.SignedIn{}, err
	}
	u, g, err := s.API.Whoami(base, token)
	if err != nil {
		return hosting.SignedIn{}, err
	}
	if err := hosting.SameUser(a, u.Login); err != nil {
		return hosting.SignedIn{}, err
	}
	return hosting.SignedIn{Account: a.ID, Host: a.Host, User: u, Grant: g}, nil
}
