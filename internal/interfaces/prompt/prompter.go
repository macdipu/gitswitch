package prompt

import (
	"os"
	"path/filepath"
	"time"

	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/filesystem"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

// Prompter asks on the user's terminal, or with a popup. It implements the
// ssh domain's Prompter for the hook.
type Prompter struct{}

// OpenTerminal opens the user's terminal; nil when git runs without one or
// was told not to ask there (GIT_TERMINAL_PROMPT=0).
func (Prompter) OpenTerminal() ssh.Conversation {
	if os.Getenv("GIT_TERMINAL_PROMPT") == "0" {
		return nil
	}
	t := terminal.OpenTerminal()
	if t == nil {
		return nil // an untyped nil: the hook checks for it
	}
	return conversation{t}
}

func (Prompter) Dialog(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	return Dialog(label, cands, def, timeout)
}

// Lock holds a lock file in the user's cache folder while one gitswitch
// asks, so parallel ssh runs never ask at the same time.
func (Prompter) Lock(wait int) (unlock func()) {
	unlock, _ = filesystem.LockFile(filepath.Join(filesystem.CacheDir(), "prompt.lock"), time.Duration(wait)*time.Second)
	return unlock
}

type conversation struct{ t *terminal.Terminal }

func (c conversation) Ask(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	return Terminal(c.t, label, cands, def, timeout)
}

func (c conversation) Close() { c.t.Close() }
