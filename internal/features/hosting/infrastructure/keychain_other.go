//go:build !darwin && !windows

package infrastructure

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// keychain uses the Secret Service (GNOME Keyring, KWallet) through
// secret-tool. The token goes to it on stdin, never on the command line.
type keychain struct{}

func secretTool() (string, error) {
	p, err := exec.LookPath("secret-tool")
	if err != nil {
		return "", errors.New("no keychain found: install secret-tool (the libsecret-tools package on Debian and Ubuntu, libsecret on Fedora and Arch) and a keyring such as GNOME Keyring")
	}
	return p, nil
}

func (keychain) Get(id string) (string, error) {
	st, err := secretTool()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(st, "lookup", "service", service, "account", key(id)).Output()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee) && len(out) == 0:
		return "", hosting.ErrNoToken // secret-tool exits 1 when nothing matches
	case err != nil:
		return "", fmt.Errorf("cannot read the keychain: %v", err)
	case len(out) == 0:
		return "", hosting.ErrNoToken
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (keychain) Set(id, token string) error {
	st, err := secretTool()
	if err != nil {
		return err
	}
	cmd := exec.Command(st, "store", "--label=GitSwitch: "+id, "service", service, "account", key(id))
	cmd.Stdin = strings.NewReader(token)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot save the token in the keychain: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (keychain) Delete(id string) error {
	st, err := secretTool()
	if err != nil {
		return err
	}
	if out, err := exec.Command(st, "clear", "service", service, "account", key(id)).CombinedOutput(); err != nil && len(out) > 0 {
		return fmt.Errorf("cannot remove the token from the keychain: %s", strings.TrimSpace(string(out)))
	}
	return nil // clear exits 1 when there was nothing to remove
}
