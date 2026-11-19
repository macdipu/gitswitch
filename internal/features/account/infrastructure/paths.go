package infrastructure

import (
	"path/filepath"

	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// DefaultKeyPath is where gitswitch creates a new key for an account.
func DefaultKeyPath(id string) string {
	return filepath.Join(filesystem.HomeDir(), ".ssh", "id_ed25519_gitswitch_"+id)
}

// IdentityFile is the git include file holding an account's name and email.
func IdentityFile(id string) string {
	return filepath.Join(filesystem.ConfigDir(), "accounts", id+".gitconfig")
}

// SignersFile is the allowed signers file of an account that signs with
// ssh: its email and public key, so git can verify its signatures here.
func SignersFile(id string) string {
	return filepath.Join(filesystem.ConfigDir(), "signers", id+".allowed")
}
