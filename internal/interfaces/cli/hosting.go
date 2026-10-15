package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	hosting "github.com/macdipu/gitswitch/internal/features/hosting/domain"
	"github.com/macdipu/gitswitch/internal/platform/terminal"
)

const signinUsage = "gitswitch signin <account> (the token is asked for, or read from standard input) | signin [--check] [--json] | signout <account>"

// cmdSignin signs an account in to GitHub with a token, or lists which
// accounts are signed in (--check also asks GitHub that each token works).
func (x *CLI) cmdSignin(args []string) error {
	pos, fl := splitArgs(args)
	if len(pos) == 0 {
		return x.signinStatus(hasFlag(fl, "--check"), hasFlag(fl, "--json"))
	}
	if len(pos) != 1 {
		return usageErr(signinUsage)
	}
	a, err := x.app.Accounts.Get(pos[0])
	if err != nil {
		return err
	}
	if _, err := hosting.APIBase(a.Host); err != nil {
		return err
	}
	tok, err := x.readToken(a.ID, a.Host)
	if err != nil {
		return err
	}
	if x.app.DryRun() {
		x.say("would sign", a.ID, "in to", a.Host)
		return nil
	}
	s, err := x.app.Hosting.SignIn(a.ID, tok)
	if err != nil {
		return err
	}
	x.sayf("Signed in: %s uses %s's token on %s. It is kept in your keychain, not in git config.\n", s.Account, s.Login, s.Host)
	if s.Expires != "" {
		x.say("The token expires", s.Expires+".")
	}
	if s.HTTPS == "" {
		x.sayf("Git now uses it for HTTPS remotes of %s's repos on %s; other repos keep your own credentials.\n", s.Account, s.Host)
	} else {
		x.say("HTTPS remotes:", s.HTTPS)
	}
	return nil
}

// cmdCredential is git's credential helper protocol: "get" answers with
// the token of the repo's signed-in account, or nothing, so git asks your
// own helpers. GitSwitch keeps no other credentials: store and erase do
// nothing. It never prompts, never fails and prints only the answer.
func (x *CLI) cmdCredential(args []string) error {
	in, _ := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	if len(args) != 1 || args[0] != "get" {
		return nil
	}
	r := hosting.ParseRequest(string(in))
	if user, pass, ok := x.app.Hosting.Credential(r); ok {
		fmt.Fprintf(x.out, "username=%s\npassword=%s\n", user, pass)
	}
	return nil
}

// readToken reads a token without showing it, or from standard input when
// that is not a terminal (gh auth token | gitswitch signin work).
func (x *CLI) readToken(id, host string) (string, error) {
	st, err := terminal.MakeRaw()
	if err != nil { // piped
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			return "", errors.New("no token on standard input; run it in a terminal to be asked for one")
		}
		return line, nil
	}
	defer terminal.RestoreTTY(st)
	fmt.Fprintf(x.out, "Create a token for %s while signed in to %s as that account:\r\n  %s\r\n", id, host, hosting.TokenPage(host))
	fmt.Fprint(x.out, "A fine-grained token with \"All repositories\" and read access to Metadata and Contents is enough.\r\n")
	fmt.Fprint(x.out, "Paste the token (it is not shown) and press Enter: ")
	var b []byte
	one := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(one); err != nil {
			return "", err
		}
		switch c := one[0]; c {
		case '\r', '\n':
			fmt.Fprint(x.out, "\r\n")
			return string(b), nil
		case 3, 27: // Ctrl+C, Esc
			fmt.Fprint(x.out, "\r\n")
			return "", errors.New("cancelled")
		case 8, 127: // Backspace
			if len(b) > 0 {
				b = b[:len(b)-1]
			}
		default:
			if c >= ' ' {
				b = append(b, c)
			}
		}
	}
}

// signinStatus lists each account's sign-in, from the keychain, or with
// check from GitHub.
func (x *CLI) signinStatus(check, asJSON bool) error {
	sts := x.app.Hosting.Status()
	type row struct {
		hosting.Status
		Login string `json:"login,omitempty"`
		Check string `json:"check,omitempty"` // why the token does not work
	}
	rows := make([]row, len(sts))
	for i, s := range sts {
		rows[i].Status = s
		if check && s.SignedIn {
			if si, err := x.app.Hosting.Check(s.Account); err != nil {
				rows[i].Check = err.Error()
			} else {
				rows[i].Login = si.Login
			}
		}
	}
	if asJSON {
		return json.NewEncoder(x.out).Encode(rows)
	}
	if len(rows) == 0 {
		x.say("No accounts yet.")
		return nil
	}
	bad := false
	for _, r := range rows {
		var what string
		switch {
		case !r.Supported:
			what = "no sign-in for " + r.Host + " yet"
		case r.Error != "":
			what, bad = "keychain: "+r.Error, true
		case !r.SignedIn:
			what = "not signed in (gitswitch signin " + r.Account + ")"
		case r.Check != "":
			what, bad = "token does not work: "+r.Check, true
		case r.Login != "":
			what = "signed in as " + r.Login + ", token works"
		default:
			what = "signed in"
		}
		x.sayf("%-14s %-16s %s\n", r.Account, r.Host, what)
	}
	if bad {
		return errors.New("some tokens do not work; sign in again with: gitswitch signin <account>")
	}
	return nil
}

func (x *CLI) cmdSignout(args []string) error {
	if len(args) != 1 {
		return usageErr("gitswitch signout <account>")
	}
	if x.app.DryRun() {
		x.say("would remove the token of", args[0])
		return nil
	}
	if err := x.app.Hosting.SignOut(args[0]); err != nil {
		return err
	}
	x.say("Signed out: the token is removed from your keychain. To revoke it too, delete it on GitHub (Settings → Developer settings → Personal access tokens).")
	return nil
}
