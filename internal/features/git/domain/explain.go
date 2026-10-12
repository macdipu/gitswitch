package domain

import (
	"regexp"
	"strings"
)

// Hint explains a git or ssh error in plain words: what went wrong and
// what to do about it. The front ends show it above the raw message, which
// stays available for people who want the details.
type Hint struct {
	Problem string `json:"problem"` // what went wrong, in one sentence
	Fix     string `json:"fix"`     // what to do next
	// Action names a step the app can offer as a button: "pubkey" (show
	// the account's public key and where to add it), "test" (test the
	// account again) or "" for none.
	Action string `json:"action,omitempty"`
}

type explainer struct {
	match *regexp.Regexp
	hint  func(m []string) Hint
}

func fixed(h Hint) func([]string) Hint { return func([]string) Hint { return h } }

// explainers are tried in order; the first match wins, so more specific
// messages come before general ones.
var explainers = []explainer{
	{regexp.MustCompile(`(?i)REMOTE HOST IDENTIFICATION HAS CHANGED`), fixed(Hint{
		Problem: "The server's identity is different from the one your computer saved last time. Usually the host changed its key, but it can also mean someone is intercepting the connection.",
		Fix:     "Check the host's published SSH fingerprints (for GitHub: docs.github.com, \"GitHub's SSH key fingerprints\"). If they match, remove the old entry with `ssh-keygen -R <host>` and connect again.",
	})},
	{regexp.MustCompile(`(?i)Host key verification failed|authenticity of host .* can't be established`), fixed(Hint{
		Problem: "Your computer has not confirmed this server's identity yet, so ssh refused to connect.",
		Fix:     "Run `gitswitch test` in a terminal once and answer yes when asked about the host. After that it works everywhere, IDEs included.",
		Action:  "test",
	})},
	{regexp.MustCompile(`(?i)UNPROTECTED PRIVATE KEY FILE|bad permissions`), fixed(Hint{
		Problem: "Other users on this computer can read the key file, so ssh refuses to use it.",
		Fix:     "Make it private: `chmod 600 <key file>` (on Windows, remove other users from the file's Security settings).",
	})},
	{regexp.MustCompile(`(?i)no such identity|identity file .* (not accessible|no such file)`), fixed(Hint{
		Problem: "The key file set for this account does not exist any more.",
		Fix:     "Pick another key for the account, or create a new one, then add its public key on the host.",
	})},
	{regexp.MustCompile(`(?i)incorrect passphrase|bad passphrase|passphrase .*(wrong|incorrect)`), fixed(Hint{
		Problem: "The key's passphrase was not accepted.",
		Fix:     "Try again and type the passphrase you chose when the key was made. If it is lost, create a new key for the account.",
	})},
	{regexp.MustCompile(`(?i)Too many authentication failures`), fixed(Hint{
		Problem: "ssh offered too many keys and the server stopped listening before the right one.",
		Fix:     "Make sure the account has a key set (gitswitch then offers only that one), or remove unused keys from your ssh agent with `ssh-add -D`.",
	})},
	{regexp.MustCompile(`(?i)Permission denied \(publickey`), fixed(Hint{
		Problem: "The server does not recognise this account's key: it has not been added to the account on the host yet, or it was added to a different account.",
		Fix:     "Add the public key on the host's SSH keys page while signed in as this account, then test again.",
		Action:  "pubkey",
	})},
	{regexp.MustCompile(`(?i)Permission to (\S+) denied to (\S+?)\.?(\s|$)`), func(m []string) Hint {
		return Hint{
			Problem: "You are signed in as " + m[2] + ", which cannot push to " + m[1] + ".",
			Fix:     "Use the account that has write access to this repo (`gitswitch use <account>`), or ask the owner to give " + m[2] + " access.",
		}
	}},
	{regexp.MustCompile(`(?i)Repository not found|does not appear to be a git repository|project you were looking for could not be found|repository .* does not exist`), fixed(Hint{
		Problem: "The host found no repo at this address that this account can see. Either the address is wrong, or the repo is private and this account has no access.",
		Fix:     "Check the owner and repo name. If they are right, use the account that has access: `gitswitch use <account>` in the repo, or choose it in the app.",
	})},
	{regexp.MustCompile(`(?i)Could not resolve hostname|Name or service not known|nodename nor servname|No such host is known|Temporary failure in name resolution`), fixed(Hint{
		Problem: "The host name could not be found.",
		Fix:     "Check the host name for typos and that you are online.",
	})},
	{regexp.MustCompile(`(?i)Connection timed out|Operation timed out|Network is unreachable|Connection refused|kex_exchange_identification|Connection (closed|reset) by`), fixed(Hint{
		Problem: "The connection to the host failed. A firewall or proxy may be blocking SSH (port 22), or you are offline.",
		Fix:     "Check your connection. On networks that block port 22, GitHub also accepts SSH on port 443: set the account's host to ssh.github.com.",
	})},
	{regexp.MustCompile(`(?i)Please tell me who you are|Author identity unknown|empty ident name|no email was given and auto-detection is disabled`), fixed(Hint{
		Problem: "This repo has no account yet, so git does not know which name and email to commit with.",
		Fix:     "Choose the account for this repo: `gitswitch use <account>` in the repo, or pick it in the app's Repos page.",
	})},
	{regexp.MustCompile(`(?i)could not lock config file`), fixed(Hint{
		Problem: "Another program was changing git's settings at the same moment.",
		Fix:     "Try again in a moment. If it keeps happening, look for a leftover .lock file next to your .gitconfig and delete it.",
	})},
	{regexp.MustCompile(`(?i)local changes .* would be overwritten|untracked working tree files would be overwritten|Please commit your changes or stash them`), fixed(Hint{
		Problem: "You have changes that are not committed yet, and this would overwrite them.",
		Fix:     "Commit your changes first, or put them aside with `git stash` and bring them back afterwards with `git stash pop`.",
	})},
	{regexp.MustCompile(`(?i)\[rejected\].*\((fetch first|non-fast-forward)\)|Updates were rejected because`), fixed(Hint{
		Problem: "The remote has commits you don't have yet, so your push was refused.",
		Fix:     "Get them first (Update in the app, or `git pull`), then push again.",
	})},
	{regexp.MustCompile(`(?i)Not possible to fast-forward|have diverged|divergent branches`), fixed(Hint{
		Problem: "Your branch and the remote both have new commits, so they can't simply be lined up.",
		Fix:     "Combine them in a terminal: `git pull --rebase` puts your commits on top, `git pull --no-rebase` makes a merge commit.",
	})},
	{regexp.MustCompile(`(?i)CONFLICT \(|Merge conflict|could not apply`), fixed(Hint{
		Problem: "The change clashes with other changes to the same lines.",
		Fix:     "gitswitch stopped and put everything back, so nothing changed. To make this change anyway, do it in a terminal or your editor and resolve the clashing lines by hand.",
	})},
}

// Explain returns a plain-words explanation of a git or ssh error message,
// or nil when the message is not one it knows.
func Explain(msg string) *Hint {
	if strings.TrimSpace(msg) == "" {
		return nil
	}
	for _, e := range explainers {
		if m := e.match.FindStringSubmatch(msg); m != nil {
			h := e.hint(m)
			return &h
		}
	}
	return nil
}
