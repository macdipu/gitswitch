package infrastructure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// Cache keeps each account's repo list as JSON in gitswitch's cache
// folder: repo names and descriptions, never tokens.
type Cache struct {
	Dir string // "": <cache folder>/remote-repos
}

func (c Cache) path(id string) string {
	d := c.Dir
	if d == "" {
		d = filepath.Join(filesystem.CacheDir(), "remote-repos")
	}
	return filepath.Join(d, strings.ToLower(id)+".json")
}

func (c Cache) Load(id string) (hosting.RepoList, bool) {
	var l hosting.RepoList
	b, err := os.ReadFile(c.path(id))
	if err != nil || json.Unmarshal(b, &l) != nil {
		return hosting.RepoList{}, false
	}
	return l, true
}

func (c Cache) Save(id string, l hosting.RepoList) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path(id)), 0o700); err != nil {
		return err
	}
	return filesystem.WriteAtomic(c.path(id), b, 0o600)
}

func (c Cache) Delete(id string) error {
	if err := os.Remove(c.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
