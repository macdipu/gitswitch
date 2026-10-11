package domain

import (
	"regexp"
	"strings"
)

// AddKeyPage is the host's SSH keys settings page, or "" when unknown.
func AddKeyPage(host string) string {
	return AddKeyHelp(host).Page
}

// KeyHelp says where on the host to add a public key.
type KeyHelp struct {
	Page  string   `json:"page"`  // SSH keys settings page, "" when unknown
	Steps []string `json:"steps"` // what to do there, in order
}

// AddKeyHelp is the guide for adding a public key on the host.
func AddKeyHelp(host string) KeyHelp {
	h := strings.ToLower(host)
	signIn := "Sign in to " + h + " as the account you are adding."
	switch {
	case strings.Contains(h, "github"):
		return KeyHelp{"https://" + h + "/settings/keys", []string{signIn,
			"Open Settings → SSH and GPG keys and click New SSH key.",
			"Paste the key, give it a title (e.g. this computer's name) and click Add SSH key."}}
	case strings.Contains(h, "gitlab"):
		return KeyHelp{"https://" + h + "/-/user_settings/ssh_keys", []string{signIn,
			"Open Preferences → SSH Keys and click Add new key.",
			"Paste the key, give it a title and click Add key."}}
	case h == "bitbucket.org":
		return KeyHelp{"https://bitbucket.org/account/settings/ssh-keys/", []string{signIn,
			"Open Personal settings → SSH keys and click Add key.",
			"Paste the key, give it a label and click Add key."}}
	}
	return KeyHelp{Steps: []string{signIn, "Add the key in that account's SSH keys settings."}}
}

// TestResult is the outcome of a login test.
type TestResult struct {
	OK     bool   `json:"ok"`
	User   string `json:"user"`
	Output string `json:"output"`
}

var greetings = []*regexp.Regexp{
	regexp.MustCompile(`Hi ([^!\s]+)!`),                   // GitHub
	regexp.MustCompile(`Welcome to GitLab, @([^!\s]+)!`),  // GitLab
	regexp.MustCompile(`logged in as ([^\s.]+)`),          // Bitbucket
	regexp.MustCompile(`(?i)authenticated as ([^\s.!]+)`), // others
}

// ParseGreeting finds the logged-in username in a host's ssh greeting.
func ParseGreeting(out string) string {
	for _, re := range greetings {
		if m := re.FindStringSubmatch(out); m != nil {
			return strings.TrimPrefix(m[1], "@")
		}
	}
	return ""
}

// Existing is a key pair found in ~/.ssh.
type Existing struct {
	Path    string `json:"path"`
	Comment string `json:"comment"`
	UsedBy  string `json:"usedBy"`
}
