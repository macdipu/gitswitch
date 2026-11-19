package infrastructure

import "strings"

// NoCommits reports whether err is git saying the repo has no commit yet
// (an unborn branch). git words it differently by version: "does not have
// any commits yet", "unknown revision", or "bad revision 'HEAD'" (git 2.47).
func NoCommits(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	for _, s := range []string{
		"does not have any commits",
		"unknown revision",
		"bad revision 'HEAD'",
		"ambiguous argument 'HEAD'",
		"bad default revision 'HEAD'",
		"Needed a single revision",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}
