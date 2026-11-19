package application

import (
	"errors"
	"fmt"
	"strings"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
)

// coAuthorCount is how many co-author suggestions CommitInfo gives.
const coAuthorCount = 50

// CommitInfo is what the commit box offers: the last n (0: 10) distinct
// messages you wrote in the repo (your email, every branch), the message
// of the last commit that failed, the sign-off setting, the authors of the
// recent commits to pick co-authors from (not you, nor your other
// accounts), and the name and email the commit will show.
func (s *Service) CommitInfo(gitDir string, n int) (workspace.CommitInfo, error) {
	if n <= 0 {
		n = workspace.RecentCount
	}
	me := s.identity(gitDir)
	info := workspace.CommitInfo{Recent: []workspace.Recent{}, CoAuthors: []workspace.CoAuthor{}, Identity: me}
	if s.Messages == nil {
		return info, nil
	}
	info.SignoffSetting = s.SignoffSetting(gitDir)
	info.Signoff = info.SignoffSetting.Mode
	info.Draft = s.Messages.Draft(gitDir)
	if me.Email != "" {
		// Read more than n: the same message written twice counts once.
		list, err := s.Messages.Recent(gitDir, me.Email, n*5)
		if err != nil {
			return info, err
		}
		info.Recent = workspace.RecentMessages(list, n)
	}
	authors, err := s.Messages.Authors(gitDir, workspace.AuthorsScanned)
	if err != nil {
		return info, err
	}
	exclude := []string{me.Email}
	for _, a := range s.Accounts.Load().Accounts {
		exclude = append(exclude, a.Email)
	}
	info.CoAuthors = workspace.CoAuthors(authors, exclude)
	if len(info.CoAuthors) > coAuthorCount {
		info.CoAuthors = info.CoAuthors[:coAuthorCount]
	}
	return info, nil
}

// SignoffSetting says whether commits in a repo get a Signed-off-by
// trailer (gitDir "": the setting for every repo).
func (s *Service) SignoffSetting(gitDir string) workspace.SignoffSetting {
	if s.Messages == nil {
		return workspace.SignoffSetting{}
	}
	return workspace.NewSignoffSetting(s.Messages.SignoffValues(gitDir))
}

// SetSignoffSetting saves whether commits get a Signed-off-by trailer:
// "on" or "off", for every repo when gitDir is "", else for that repo
// only, where "" follows the setting for every repo again.
func (s *Service) SetSignoffSetting(gitDir, value string) error {
	if s.Messages == nil {
		return errors.New("the sign-off setting is not available here")
	}
	v, ok := workspace.ParseOnOff(value)
	if !ok && (gitDir == "" || strings.TrimSpace(value) != "") {
		return fmt.Errorf("%q is not a choice: on or off", value)
	}
	where := "every repo"
	if gitDir != "" {
		where = "this repo"
	}
	if s.FS.Dry(fmt.Sprintf("sign off commits in %s: %q", where, v)) {
		return nil
	}
	return s.Messages.SaveSignoff(gitDir, v)
}

// identity is the name and email a commit in the repo shows: the repo's
// own, else its account's, else what git has.
func (s *Service) identity(gitDir string) workspace.Person {
	cfg := s.Accounts.Load()
	info := s.Repos.InfoFor(cfg, gitDir)
	p := workspace.Person{Name: strings.TrimSpace(info.UserName), Email: strings.TrimSpace(info.Email)}
	if a, _ := s.Repos.AccountFor(cfg, gitDir); a != nil {
		if p.Name == "" {
			p.Name = a.Name
		}
		if p.Email == "" {
			p.Email = a.Email
		}
	}
	if (p.Name == "" || p.Email == "") && s.Messages != nil {
		g := s.Messages.Identity(gitDir)
		if p.Name == "" {
			p.Name = g.Name
		}
		if p.Email == "" {
			p.Email = g.Email
		}
	}
	return p
}

// withTrailers adds the co-authors and, when asked (o.Signoff, else with
// useSetting the setting), the sign-off to message.
func (s *Service) withTrailers(gitDir, message string, o workspace.CommitOptions, useSetting bool) (string, error) {
	signoff := false
	if o.Signoff != nil {
		signoff = *o.Signoff
	} else if useSetting {
		signoff = s.SignoffSetting(gitDir).Mode
	}
	want := signoff
	for _, c := range o.CoAuthors {
		want = want || strings.TrimSpace(c) != ""
	}
	if !want {
		return message, nil
	}
	ts, err := workspace.Trailers(s.identity(gitDir), signoff, o.CoAuthors)
	if err != nil {
		return message, err
	}
	return workspace.WithTrailers(message, ts), nil
}

// keepDraft keeps the message of a commit that failed, so it is not lost,
// and clears it after one that succeeded. Nothing is kept in a dry run.
func (s *Service) keepDraft(gitDir, message string, err error) {
	if s.Messages == nil || (s.FS != nil && s.FS.DryRun) {
		return
	}
	switch m := workspace.Message(message); {
	case err == nil:
		_ = s.Messages.SaveDraft(gitDir, "")
	case m != "":
		_ = s.Messages.SaveDraft(gitDir, m)
	}
}
