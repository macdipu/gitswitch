// Package application holds the account use cases: add, edit, rename and
// remove accounts, their keys, rules and settings, and the account saved
// for a repo. It depends only on domain ports. Import it as accountapp.
package application

import (
	"fmt"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	repo "github.com/macdipu/gitswitch/internal/features/repository/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
)

// Service runs the account use cases.
type Service struct {
	Store        account.Store
	RepoAccounts account.RepoAccounts
	Keys         ssh.KeyTool
	Repos        repo.Reader // repos to update after a rename
}

// Config returns the current settings, read fresh.
func (s *Service) Config() *account.Config { return s.Store.Load() }

// Get returns an account by id (following renames) or an error.
func (s *Service) Get(id string) (*account.Account, error) { return s.Config().Lookup(id) }

// SuggestedName is the commit name to offer for a new account.
func (s *Service) SuggestedName() string { return s.Store.SuggestedName() }

// Add creates an account and, if asked, its key.
func (s *Service) Add(acc account.Account, k account.KeyChoice) (account.Account, error) {
	if s.Config().Account(acc.ID) != nil {
		return acc, fmt.Errorf("an account called %s already exists", acc.ID)
	}
	if acc.Email == "" {
		return acc, fmt.Errorf("commit email is required")
	}
	if acc.Name == "" {
		acc.Name = s.SuggestedName()
	}
	if err := s.Store.Save(acc); err != nil {
		return acc, err
	}
	if k.New {
		p, err := s.Keys.Generate(&acc, k.Interactive, k.Passphrase)
		if err != nil {
			return acc, err
		}
		acc.Key = p
		if err := s.Store.Save(acc); err != nil {
			return acc, err
		}
	}
	return acc, nil
}

// Update saves changed fields of an existing account.
func (s *Service) Update(acc account.Account) error {
	old, err := s.Get(acc.ID)
	if err != nil {
		return err
	}
	acc.ID = old.ID
	if acc.User == "" {
		acc.User = old.User
	}
	return s.Store.Save(acc)
}

// Remove removes an account and its rules; with deleteKeys also its key
// files. It returns the deleted files.
func (s *Service) Remove(id string, deleteKeys bool) ([]string, error) {
	acc, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	copyOf := *acc
	if err := s.Store.Remove(copyOf.ID); err != nil {
		return nil, err
	}
	if deleteKeys {
		return s.Store.DeleteKeyFiles(&copyOf), nil
	}
	return nil, nil
}

// Rename renames an account everywhere and updates repos in the project
// folders. It returns how many repos were updated.
func (s *Service) Rename(oldID, newID string) (int, error) {
	acc, err := s.Get(oldID)
	if err != nil {
		return 0, err
	}
	oldID = acc.ID
	if err := s.Store.Rename(oldID, newID); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range s.Repos.List(s.Config()) {
		if strings.EqualFold(r.Saved, oldID) && s.RepoAccounts.SaveRepoAccountID(r.GitDir, newID) == nil {
			n++
		}
	}
	return n, nil
}

// SetDefault sets the account used when nothing else decides.
func (s *Service) SetDefault(id string) error { return s.Store.SetDefault(id) }

// GenerateKey creates a new key for an existing account.
func (s *Service) GenerateKey(id string, interactive bool, passphrase string) (string, error) {
	acc, err := s.Get(id)
	if err != nil {
		return "", err
	}
	p, err := s.Keys.Generate(acc, interactive, passphrase)
	if err != nil {
		return "", err
	}
	acc.Key = p
	return p, s.Store.Save(*acc)
}

// UseKey points an account at an existing key file.
func (s *Service) UseKey(id, path string) error {
	acc, err := s.Get(id)
	if err != nil {
		return err
	}
	acc.Key = path
	return s.Store.Save(*acc)
}

func (s *Service) AddRule(r account.Rule) error    { return s.Store.AddRule(r) }
func (s *Service) RemoveRule(r account.Rule) error { return s.Store.RemoveRule(r) }

// RemoveRuleAt removes the rule at a 0-based position in Config().Rules.
func (s *Service) RemoveRuleAt(i int) error {
	rules := s.Config().Rules
	if i < 0 || i >= len(rules) {
		return fmt.Errorf("no such rule")
	}
	return s.Store.RemoveRule(rules[i])
}

func (s *Service) SetSetting(key, value string) error { return s.Store.SetSetting(key, value) }
func (s *Service) AddScanFolder(p string) error       { return s.Store.AddScanFolder(p) }
func (s *Service) RemoveScanFolder(p string) error    { return s.Store.RemoveScanFolder(p) }

// SetRepoAccount saves an account for a repo ("" makes it ask again). It
// returns a removed repo-level core.sshCommand, if there was one.
func (s *Service) SetRepoAccount(gitDir, id string) (string, error) {
	if id == "" {
		return "", s.RepoAccounts.ForgetRepo(gitDir)
	}
	acc, err := s.Get(id)
	if err != nil {
		return "", err
	}
	return s.RepoAccounts.SetRepoAccount(gitDir, acc)
}

// UseOnce applies an account's name and email without saving the choice.
func (s *Service) UseOnce(gitDir, id string) error {
	acc, err := s.Get(id)
	if err != nil {
		return err
	}
	s.RepoAccounts.ApplyIdentity(gitDir, acc)
	return nil
}
