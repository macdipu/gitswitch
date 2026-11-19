package domain

import (
	"fmt"
	"strings"
	"unicode"
)

// Signing formats of an account's commits.
const (
	SigningOff = ""    // commits are not signed by gitswitch
	SigningSSH = "ssh" // gpg.format=ssh: signed with an ssh key (GitHub and GitLab verify it)
	SigningGPG = "gpg" // gpg.format=openpgp: signed with a gpg key id
)

// ParseSigningFormat reads what a person types: ssh, gpg (or openpgp), or
// off (none, no, empty). An unknown word is an error.
func ParseSigningFormat(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ssh":
		return SigningSSH, nil
	case "gpg", "openpgp", "pgp":
		return SigningGPG, nil
	case "", "off", "none", "no", "false":
		return SigningOff, nil
	}
	return "", fmt.Errorf("signing format %q: use ssh, gpg or off", v)
}

// IsLiteralSSHKey reports whether a signing key is the key itself
// ("key::ssh-ed25519 AAAA..." or "ssh-ed25519 AAAA...") and not a path.
func IsLiteralSSHKey(v string) bool {
	v = strings.TrimSpace(v)
	return strings.HasPrefix(v, "key::") || strings.HasPrefix(v, "ssh-") ||
		strings.HasPrefix(v, "ecdsa-") || strings.HasPrefix(v, "sk-")
}

// InferSigningFormat guesses the format from a signing key: a path or a
// literal public key is ssh, anything else (a key id) is gpg.
func InferSigningFormat(key string) string {
	k := strings.TrimSpace(key)
	if IsLiteralSSHKey(k) || strings.ContainsAny(k, `/\`) || strings.HasPrefix(k, "~") || strings.HasSuffix(k, ".pub") {
		return SigningSSH
	}
	return SigningGPG
}

// Signs reports whether the account signs its commits.
func (a Account) Signs() bool { return a.SigningFormat != SigningOff }

// EffectiveSigningKey is the key that signs: the signing key, or for ssh
// with none given, the account's own public key file.
func (a Account) EffectiveSigningKey() string {
	if k := strings.TrimSpace(a.SigningKey); k != "" {
		return k
	}
	if a.SigningFormat == SigningSSH && a.Key != "" {
		if strings.HasSuffix(a.Key, ".pub") {
			return a.Key
		}
		return a.Key + ".pub"
	}
	return ""
}

// ValidateSigning checks the signing fields of an account.
func (a Account) ValidateSigning() error {
	for _, f := range [][2]string{{"signing key", a.SigningKey}, {"signing format", a.SigningFormat}} {
		if strings.IndexFunc(f[1], unicode.IsControl) >= 0 {
			return fmt.Errorf("the %s contains a line break or other control character", f[0])
		}
	}
	switch a.SigningFormat {
	case SigningOff:
		return nil
	case SigningSSH, SigningGPG:
	default:
		return fmt.Errorf("signing format %q: use ssh, gpg or off", a.SigningFormat)
	}
	if a.EffectiveSigningKey() == "" {
		if a.SigningFormat == SigningSSH {
			return fmt.Errorf("signing with ssh needs a key: give --signing-key <public key file>, or give %s a key first", a.ID)
		}
		return fmt.Errorf("signing with gpg needs a key id: give --signing-key <key id>")
	}
	return nil
}

// AllowedSignersLine is the line of git's allowed signers file that makes
// signatures made with pub verify for email: "<email> namespaces=\"git\" <type> <key>".
func AllowedSignersLine(email, pub string) string {
	f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(pub), "key::"))
	if email == "" || len(f) < 2 {
		return ""
	}
	return email + ` namespaces="git" ` + f[0] + " " + f[1] + "\n"
}

// SigningHelp says where to register the account's PUBLIC signing key on
// its host. Signing keys are separate from the keys that log in.
func SigningHelp(host, format string) (page string, steps []string) {
	h := strings.ToLower(host)
	if format == SigningGPG {
		switch {
		case strings.Contains(h, "github"):
			return "https://" + h + "/settings/keys", []string{"Open Settings → SSH and GPG keys → New GPG key and paste the output of: gpg --armor --export <key id>."}
		case strings.Contains(h, "gitlab"):
			return "https://" + h + "/-/user_settings/gpg_keys", []string{"Open Preferences → GPG Keys and add the output of: gpg --armor --export <key id>."}
		}
		return "", []string{"Add the public key (gpg --armor --export <key id>) in that account's GPG key settings on " + host + "."}
	}
	switch {
	case strings.Contains(h, "github"):
		return "https://" + h + "/settings/ssh/new", []string{
			"Open Settings → SSH and GPG keys → New SSH key.",
			"Set Key type to \"Signing Key\" (a key added only for login does not verify commits), paste the public key and save."}
	case strings.Contains(h, "gitlab"):
		return "https://" + h + "/-/user_settings/ssh_keys", []string{
			"Open Preferences → SSH Keys → Add new key.",
			"Paste the public key and set Usage type to \"Signing\" or \"Authentication & Signing\"."}
	}
	return "", []string{"Add the public key as a signing key in that account's SSH key settings on " + host + "."}
}
