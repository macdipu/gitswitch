package infrastructure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
)

// signingFlag marks the signing settings in a repo as written by gitswitch.
const signingFlag = "gitswitch.signing"

// signingKeys are the repo settings the flag stands for.
var signingKeys = []string{"commit.gpgsign", "gpg.format", "user.signingkey", "gpg.ssh.allowedsignersfile"}

// SigningKeyFile is the file the account's ssh signing key is read from
// (the public key file), or "" for a key given in full or for gpg.
func SigningKeyFile(a account.Account) string {
	k := a.EffectiveSigningKey()
	if a.SigningFormat != account.SigningSSH || k == "" || account.IsLiteralSSHKey(k) {
		return ""
	}
	return filepath.Clean(filesystem.ExpandHome(k))
}

// PublicSigningKey returns the public key of an ssh signing account (one
// "type base64" line), read from the key file or given in full. It never
// reads a private key: for a private key path it reads the .pub beside it.
func (s *Store) PublicSigningKey(a account.Account) (string, error) {
	return PublicSigningKey(a)
}

// PublicSigningKey: see Store.PublicSigningKey.
func PublicSigningKey(a account.Account) (string, error) {
	if a.SigningFormat != account.SigningSSH {
		return "", errors.New("only ssh signing keys have a public key file")
	}
	k := a.EffectiveSigningKey()
	if k == "" {
		return "", errors.New("no signing key")
	}
	if account.IsLiteralSSHKey(k) {
		f := strings.Fields(strings.TrimPrefix(k, "key::"))
		if len(f) < 2 {
			return "", errors.New("the signing key is not a public ssh key")
		}
		return f[0] + " " + f[1], nil
	}
	p := filepath.Clean(filesystem.ExpandHome(k))
	if !strings.HasSuffix(p, ".pub") {
		p += ".pub" // never open the private key
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("cannot read the public signing key %s", p)
	}
	f := strings.Fields(string(b))
	if len(f) < 2 || !strings.Contains(f[0], "-") {
		return "", fmt.Errorf("%s does not hold a public ssh key", p)
	}
	return f[0] + " " + f[1], nil
}

// writeSignersFile writes the account's allowed signers file (ssh signing)
// or removes it. Best effort: a missing key file only means local
// verification has nothing to check against.
func (s *Store) writeSignersFile(a account.Account) {
	if a.SigningFormat != account.SigningSSH {
		if _, err := os.Stat(SignersFile(a.ID)); err == nil {
			_ = s.FS.Remove(SignersFile(a.ID))
		}
		return
	}
	pub, err := PublicSigningKey(a)
	if err != nil {
		return
	}
	line := account.AllowedSignersLine(a.Email, pub)
	if line == "" {
		return
	}
	if old, err := os.ReadFile(SignersFile(a.ID)); err == nil && string(old) == line {
		return
	}
	_ = s.FS.WriteFile(SignersFile(a.ID), []byte(line), 0o644)
}

// applySigning keeps a repo's signing settings in step with the account:
// signed commits with its key, or none. It only touches settings it wrote
// itself (gitswitch.signing=true) or a repo that has none of its own, so a
// signing setup you made in the repo stays.
func (s *Store) applySigning(gitDir string, a *account.Account) {
	have := map[string]string{}
	out, _ := s.Git.Run("--git-dir="+gitDir, "config", "--local", "-z", "--get-regexp",
		`^(gitswitch\.signing|commit\.gpgsign|gpg\.format|user\.signingkey|gpg\.ssh\.allowedsignersfile)$`)
	for _, e := range strings.Split(out, "\x00") {
		if k, v, ok := strings.Cut(e, "\n"); ok {
			have[k] = v
		}
	}
	ours := account.ParseBool(have[signingFlag], false)
	if !ours && len(have) > 0 {
		return // signing set up in this repo by someone else
	}
	if !a.Signs() {
		if ours {
			for _, k := range append(signingKeys, signingFlag) {
				if _, ok := have[k]; ok {
					_ = s.Git.RepoUnset(gitDir, k)
				}
			}
		}
		return
	}
	want := map[string]string{"commit.gpgsign": "true", "user.signingkey": signingKeyValue(*a)}
	if a.SigningFormat == account.SigningSSH {
		want["gpg.format"] = "ssh"
		if _, err := os.Stat(SignersFile(a.ID)); err != nil {
			s.writeSignersFile(*a)
		}
		if _, err := os.Stat(SignersFile(a.ID)); err == nil {
			want["gpg.ssh.allowedsignersfile"] = filepath.ToSlash(SignersFile(a.ID))
		}
	} else {
		want["gpg.format"] = "openpgp"
	}
	for _, k := range signingKeys {
		v, set := want[k]
		switch {
		case set && have[k] != v:
			_ = s.Git.RepoSet(gitDir, k, v)
		case !set:
			if _, ok := have[k]; ok {
				_ = s.Git.RepoUnset(gitDir, k)
			}
		}
	}
	if !ours {
		_ = s.Git.RepoSet(gitDir, signingFlag, "true")
	}
}

// signingKeyValue is the value of user.signingkey for an account.
func signingKeyValue(a account.Account) string {
	k := a.EffectiveSigningKey()
	if a.SigningFormat == account.SigningSSH {
		if account.IsLiteralSSHKey(k) {
			return "key::" + strings.TrimPrefix(k, "key::")
		}
		return filepath.ToSlash(filesystem.ExpandHome(k))
	}
	return k
}
