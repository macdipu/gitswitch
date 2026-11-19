package domain

// Tools reads and starts what the external tools need from git: a file's
// versions, git's own tool settings, and `git difftool` / `git mergetool`.
type Tools interface {
	// Show is the content of a blob git names (":2:path", "HEAD:path");
	// ok is false when there is none.
	Show(worktree, spec string) (content []byte, ok bool)
	// ConfigGet reads a setting as git sees it in worktree ("" if unset).
	ConfigGet(worktree, key string) string
	// StartGit starts `git args...` in worktree and does not wait for it.
	StartGit(worktree string, args []string) error
}
