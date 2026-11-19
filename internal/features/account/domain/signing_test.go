package domain

import (
	"strings"
	"testing"
)

func TestParseSigningFormat(t *testing.T) {
	for in, want := range map[string]string{"ssh": SigningSSH, " SSH ": SigningSSH, "gpg": SigningGPG, "openpgp": SigningGPG,
		"off": SigningOff, "none": SigningOff, "": SigningOff} {
		if got, err := ParseSigningFormat(in); err != nil || got != want {
			t.Errorf("ParseSigningFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseSigningFormat("x509"); err == nil {
		t.Error("an unknown format must be refused")
	}
}

func TestInferSigningFormat(t *testing.T) {
	for in, want := range map[string]string{
		"~/.ssh/id.pub": SigningSSH, "/home/a/key": SigningSSH, `C:\k`: SigningSSH, "key::ssh-ed25519 AAA": SigningSSH,
		"ssh-ed25519 AAAA": SigningSSH, "3AA5C34371567BD2": SigningGPG, "me@x.example": SigningGPG,
	} {
		if got := InferSigningFormat(in); got != want {
			t.Errorf("InferSigningFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffectiveSigningKeyAndValidation(t *testing.T) {
	a := Account{ID: "w", Email: "w@x", Key: "~/.ssh/k_w", SigningFormat: SigningSSH}
	if got := a.EffectiveSigningKey(); got != "~/.ssh/k_w.pub" {
		t.Errorf("default ssh signing key = %q", got)
	}
	if err := a.ValidateSigning(); err != nil {
		t.Errorf("ssh with the account's key: %v", err)
	}
	a.Key = ""
	if err := a.ValidateSigning(); err == nil || !strings.Contains(err.Error(), "needs a key") {
		t.Errorf("ssh with no key: %v", err)
	}
	a.SigningFormat = SigningGPG
	if err := a.ValidateSigning(); err == nil {
		t.Error("gpg with no key id must be refused")
	}
	a.SigningKey = "ABCD1234"
	if err := a.ValidateSigning(); err != nil || a.EffectiveSigningKey() != "ABCD1234" {
		t.Errorf("gpg: %v", err)
	}
	a.SigningFormat = "x509"
	if a.ValidateSigning() == nil {
		t.Error("unknown format")
	}
	a.SigningFormat, a.SigningKey = SigningOff, "bad\nkey"
	if a.ValidateSigning() == nil {
		t.Error("a line break in the key must be refused")
	}
	if (Account{}).Signs() {
		t.Error("an empty account does not sign")
	}
}

func TestAllowedSignersLine(t *testing.T) {
	want := `me@x.example namespaces="git" ssh-ed25519 AAAAC3 ` + "\n"
	got := AllowedSignersLine("me@x.example", "key::ssh-ed25519 AAAAC3 comment")
	if got != strings.Replace(want, "AAAAC3 \n", "AAAAC3\n", 1) {
		t.Errorf("line = %q", got)
	}
	if AllowedSignersLine("", "ssh-ed25519 AAAA") != "" || AllowedSignersLine("a@b", "garbage") != "" {
		t.Error("no email or no key: no line")
	}
}

func TestSigningHelp(t *testing.T) {
	page, steps := SigningHelp("github.com", SigningSSH)
	if !strings.Contains(page, "github.com/settings/ssh/new") || !strings.Contains(strings.Join(steps, " "), "Signing Key") {
		t.Errorf("github: %q %v", page, steps)
	}
	if page, _ := SigningHelp("gitlab.example.com", SigningSSH); !strings.Contains(page, "ssh_keys") {
		t.Errorf("gitlab: %q", page)
	}
	if _, steps := SigningHelp("git.example.org", SigningGPG); len(steps) == 0 {
		t.Error("generic host needs steps")
	}
}
