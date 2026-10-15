// Package infrastructure keeps account tokens in the OS keychain (macOS
// Keychain, Windows Credential Manager, the Secret Service through
// secret-tool elsewhere) and talks to the GitHub REST API. Build tags pick
// the keychain code for each OS. Import it as hostinginfra.
package infrastructure

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// service names gitswitch's entries in the keychain.
const service = "gitswitch"

// NewSecrets returns the OS keychain, or under tests (which must never
// touch the real one) a file in gitswitch's config folder.
func NewSecrets() hosting.Secrets {
	if process.NoSystemChanges() {
		return &fileSecrets{path: filepath.Join(filesystem.ConfigDir(), "test-tokens.json")}
	}
	return keychain{}
}

// key is the keychain account name: ids are matched without case.
func key(accountID string) string { return strings.ToLower(accountID) }

// Tokens adapts the keychain to the account feature, so removing or
// renaming an account takes its token along.
type Tokens struct{ Secrets hosting.Secrets }

func (t Tokens) Delete(id string) error { return t.Secrets.Delete(id) }

func (t Tokens) Move(oldID, newID string) error {
	tok, err := t.Secrets.Get(oldID)
	if errors.Is(err, hosting.ErrNoToken) {
		return nil
	} else if err != nil {
		return err
	}
	if err := t.Secrets.Set(newID, tok); err != nil {
		return err
	}
	return t.Secrets.Delete(oldID)
}

// fileSecrets is the stand-in keychain for tests.
type fileSecrets struct {
	path string
	mu   sync.Mutex
}

func (f *fileSecrets) load() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(f.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (f *fileSecrets) save(m map[string]string) error {
	b, _ := json.Marshal(m)
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	return filesystem.WriteAtomic(f.path, b, 0o600)
}

func (f *fileSecrets) Get(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.load()[key(id)]; ok {
		return t, nil
	}
	return "", hosting.ErrNoToken
}

func (f *fileSecrets) Set(id, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.load()
	m[key(id)] = token
	return f.save(m)
}

func (f *fileSecrets) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.load()
	delete(m, key(id))
	return f.save(m)
}
