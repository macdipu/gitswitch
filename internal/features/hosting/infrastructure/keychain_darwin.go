package infrastructure

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
)

// keychain uses the login Keychain through the security tool. The token
// goes to it on stdin (security -i), never on the command line, where
// other programs could read it.
type keychain struct{}

// notFound is security's exit code for a missing item.
const notFound = 44

func (keychain) Get(id string) (string, error) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-a", key(id), "-w").Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == notFound {
		return "", hosting.ErrNoToken
	} else if err != nil {
		return "", fmt.Errorf("cannot read the Keychain: %v", err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (k keychain) Set(id, token string) error {
	cmd := exec.Command("/usr/bin/security", "-i")
	// CheckToken and ValidID allow no spaces or quotes, so the values need
	// no escaping beyond the quotes.
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"GitSwitch: %s\" -w %s\n", service, key(id), id, token))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot save the token in the Keychain: %v %s", err, strings.TrimSpace(string(out)))
	}
	// security -i reports a failed command only in its output: read it back.
	if got, err := k.Get(id); err != nil || got != token {
		return errors.New("the Keychain did not keep the token; check that the login Keychain is unlocked")
	}
	return nil
}

func (keychain) Delete(id string) error {
	err := exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", key(id)).Run()
	var ee *exec.ExitError
	if err == nil || (errors.As(err, &ee) && ee.ExitCode() == notFound) {
		return nil
	}
	return fmt.Errorf("cannot remove the token from the Keychain: %v", err)
}
