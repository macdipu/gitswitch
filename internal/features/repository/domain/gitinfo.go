package domain

import (
	"fmt"
	"regexp"
	"strconv"
)

// MinGitMajor and MinGitMinor are the oldest git GitSwitch works with
// (2.30). Some features need more: ssh signing wants 2.34, and predicting
// a merge's clashes wants 2.38 (without it the prediction is skipped).
const (
	MinGitMajor = 2
	MinGitMinor = 30
)

// GitInfo says whether git is installed and new enough.
type GitInfo struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"` // e.g. "2.43.0"; "" when unknown
	OK        bool   `json:"ok"`      // installed and at least the minimum version
	Why       string `json:"why"`     // what is wrong, and what to do; "" when OK
}

var gitVersionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// CheckGit reads the output of `git --version` ("git version 2.43.0 (Apple
// Git-146)"). err is what running it returned: git is not installed when
// it could not be started.
func CheckGit(output string, err error) GitInfo {
	if err != nil && output == "" {
		return GitInfo{Why: "git is not installed or not on your PATH. Install it from https://git-scm.com/downloads (macOS: xcode-select --install; Linux: your package manager), then open a new terminal."}
	}
	m := gitVersionRe.FindStringSubmatch(output)
	if m == nil {
		return GitInfo{Installed: true, Why: "git is installed, but its version could not be read: " + output}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	v := m[1] + "." + m[2]
	if m[3] != "" {
		v += "." + m[3]
	}
	info := GitInfo{Installed: true, Version: v, OK: true}
	if major < MinGitMajor || (major == MinGitMajor && minor < MinGitMinor) {
		info.OK = false
		info.Why = fmt.Sprintf("git %s is older than %d.%d, which GitSwitch needs. Update git: https://git-scm.com/downloads", v, MinGitMajor, MinGitMinor)
	}
	return info
}

// Initializer starts repos and reports git's version.
type Initializer interface {
	// Init runs git init in dir (made when it does not exist). branch ""
	// uses git's default initial branch. Nothing in an existing repo is
	// changed (git init there is harmless), but the caller refuses it first.
	Init(dir, branch string) error
	// Version is the output of git --version, or an error when git cannot
	// be started.
	Version() (string, error)
}
