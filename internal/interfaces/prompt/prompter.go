package prompt

import (
	account "github.com/macdipu/gitswitch/internal/features/account/domain"
	resolve "github.com/macdipu/gitswitch/internal/features/identity/domain"
	ssh "github.com/macdipu/gitswitch/internal/features/ssh/domain"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

// Prompter asks on the user's terminal, or with a popup. It implements the
// ssh domain's Prompter for the hook.
type Prompter struct{}

// OpenTerminal opens the user's terminal; nil when git runs without one.
func (Prompter) OpenTerminal() ssh.Conversation {
	t := terminal.OpenTerminal()
	if t == nil {
		return nil // an untyped nil: the hook checks for it
	}
	return conversation{t}
}

func (Prompter) Dialog(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	return Dialog(label, cands, def, timeout)
}

type conversation struct{ t *terminal.Terminal }

func (c conversation) Ask(label string, cands []account.Account, def *account.Account, timeout int) *resolve.Decision {
	return Terminal(c.t, label, cands, def, timeout)
}

func (c conversation) Close() { c.t.Close() }
