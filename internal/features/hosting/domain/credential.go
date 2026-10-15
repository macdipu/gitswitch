package domain

import (
	"strings"

	remote "github.com/macdipu/gitswitch/internal/features/git/domain"
)

// GitSwitch answers git's HTTPS credential requests as a credential helper
// only for hosts where an account is signed in. Its helpers go in a section
// of their own, credential.https://<host>/, which matches the same URLs as
// credential.https://<host> but is never one you wrote:
//
//	[credential "https://github.com/"]
//		helper =                          (start a new list here)
//		helper = !"/path/gitswitch" credential
//		helper = osxkeychain              (your helpers, as they were)
//
// So GitSwitch is asked first, and when it has no token for the repo's
// account git asks your own helpers exactly as before.

// HelperSection is the config section GitSwitch keeps for a host.
func HelperSection(host string) string {
	return "credential.https://" + remote.CanonicalHost(host) + "/"
}

// HelperCommand is the helper value for the program whose ssh hook is
// hook (the core.sshCommand value `"<path>" ssh`). ok is false when hook
// is not gitswitch's.
func HelperCommand(hook string) (string, bool) {
	hook = strings.TrimSpace(hook)
	prog, ok := strings.CutSuffix(hook, " ssh")
	if !ok || !strings.Contains(strings.ToLower(prog), "gitswitch") {
		return "", false
	}
	return "!" + prog + " credential", true
}

// HelperEntry is one credential helper setting: credential.helper (URL "")
// or credential.<URL>.helper, in the order git reads them.
type HelperEntry struct {
	URL   string
	Value string
}

// PreviousHelpers is the helper list git uses for https://<host> without
// GitSwitch's section: entries for every URL or one matching the host, an
// empty value starting the list again.
func PreviousHelpers(entries []HelperEntry, host string) []string {
	host = remote.CanonicalHost(host)
	var out []string
	for _, e := range entries {
		if e.URL != "" && (strings.EqualFold(e.URL, "https://"+host+"/") || !urlMatches(e.URL, host)) {
			continue
		}
		if e.Value == "" {
			out = nil
			continue
		}
		out = append(out, e.Value)
	}
	return out
}

// urlMatches reports whether a credential.<URL> section applies to every
// https request to host: https://host or host, with no path.
func urlMatches(u, host string) bool {
	u = strings.ToLower(u)
	if rest, ok := strings.CutPrefix(u, "https://"); ok {
		u = rest
	} else if strings.Contains(u, "://") {
		return false
	}
	if i := strings.IndexByte(u, '@'); i >= 0 {
		u = u[i+1:]
	}
	u = strings.TrimSuffix(u, "/")
	return u == host
}

// Request is what git asks a credential helper (git credential's format).
type Request struct {
	Protocol string
	Host     string
	Path     string
	Username string
}

// ParseRequest reads key=value lines up to a blank line.
func ParseRequest(s string) Request {
	var r Request
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			break
		}
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "protocol":
			r.Protocol = v
		case "host":
			r.Host = v
		case "path":
			r.Path = v
		case "username":
			r.Username = v
		}
	}
	return r
}

// TokenUser is the username sent with a token: the account's GitHub
// user, or one GitHub accepts with any token.
func TokenUser(user string) string {
	if user == "" {
		return "x-access-token"
	}
	return user
}
