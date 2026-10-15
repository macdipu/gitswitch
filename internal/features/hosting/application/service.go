// Package application signs accounts in to their host's API with a token
// kept in the OS keychain, never in git config. It depends only on domain
// ports. Import it as hostingapp.
package application

import (
	"errors"
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// Service runs the hosting use cases.
type Service struct {
	Accounts hosting.Accounts
	Secrets  hosting.Secrets
	API      hosting.API
	Helper   hosting.HelperConfig // nil: no HTTPS credentials
	Repos    hosting.Repos
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
	return hosting.SignedIn{Account: a.ID, Host: a.Host, User: u, Grant: g, HTTPS: s.syncHelper(a.Host)}, nil
}

// SignOut forgets an account's token. Signing out twice is fine.
func (s *Service) SignOut(accountID string) error {
	a, err := s.Accounts.Load().Lookup(accountID)
	if err != nil {
		return err
	}
	if err := s.Secrets.Delete(a.ID); err != nil {
		return err
	}
	if why := s.syncHelper(a.Host); why != "" && s.Helper != nil && !strings.Contains(why, "gitswitch install") {
		return errors.New("signed out, but git's credential settings could not be updated: " + why)
	}
	return nil
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

// syncHelper keeps git's credential helper section for a host: present
// while an account on the host is signed in, gone when none is. It
// returns why HTTPS remotes do not get the token, or "".
func (s *Service) syncHelper(host string) string {
	if s.Helper == nil {
		return "no credential helper here"
	}
	host = remote.CanonicalHost(host) // ssh.github.com and github.com share one section
	signed := false
	for _, st := range s.Status() {
		if st.SignedIn && remote.SameHost(st.Host, host) {
			signed = true
		}
	}
	if !signed {
		if err := s.Helper.Remove(host); err != nil {
			return err.Error()
		}
		return ""
	}
	cmd, ok := hosting.HelperCommand(s.Helper.Hook())
	if !ok {
		return "git does not run through gitswitch yet (gitswitch install), so HTTPS remotes keep using your own credentials"
	}
	entries, err := s.Helper.Entries()
	if err != nil {
		return err.Error()
	}
	values := append([]string{"", cmd}, hosting.PreviousHelpers(entries, host)...)
	if err := s.Helper.Write(host, values); err != nil {
		return err.Error()
	}
	return ""
}

// Credential answers git's "get" as a credential helper: the token of the
// signed-in account the repo uses, for an https request to its host. ok is
// false whenever GitSwitch is not sure, so git asks your own helpers.
func (s *Service) Credential(r hosting.Request) (user, password string, ok bool) {
	if r.Protocol != "https" || s.Repos == nil {
		return "", "", false
	}
	c := s.Accounts.Load()
	gd := s.Repos.Current()
	if gd == "" {
		return "", "", false
	}
	a := s.Repos.AccountFor(c, gd)
	if a == nil || !remote.SameHost(a.Host, r.Host) {
		return "", "", false
	}
	if r.Username != "" && !strings.EqualFold(r.Username, hosting.TokenUser(a.User)) {
		return "", "", false // the URL names another user
	}
	tok, err := s.Secrets.Get(a.ID)
	if err != nil {
		return "", "", false
	}
	return hosting.TokenUser(a.User), tok, true
}
