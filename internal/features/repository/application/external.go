package application

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	workspace "github.com/macdipu/gitswitch/internal/features/workspace/domain"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// OpenTerminal opens a terminal in a repo's working folder with the
// configured command (gitswitch.terminal; "" is the OS default).
func (s *Service) OpenTerminal(path string) error {
	root := s.root(path)
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("folder not found: %s", root)
	}
	return process.OpenTerminal(root, s.Lists.Load().Terminal)
}

// fileIn is the working-folder path of a repo-relative file, refusing a
// path that leaves the repo, and a file that is not there.
func (s *Service) fileIn(gitDir, rel string, mustExist bool) (wt, clean, abs string, err error) {
	clean, err = workspace.CleanPath(rel)
	if err != nil {
		return "", "", "", err
	}
	wt = s.Locator.WorktreeOf(gitDir)
	abs = filepath.Join(wt, filepath.FromSlash(clean))
	if mustExist {
		if fi, e := os.Stat(abs); e != nil || fi.IsDir() {
			return "", "", "", fmt.Errorf("%s is not in the working folder", clean)
		}
	}
	return wt, clean, abs, nil
}

// OpenFile opens one file of the repo in the configured editor (the same
// one repo open --editor uses).
func (s *Service) OpenFile(gitDir, rel string) error {
	_, _, abs, err := s.fileIn(gitDir, rel, true)
	if err != nil {
		return err
	}
	cmd, err := s.editorCommand()
	if err != nil {
		return err
	}
	return process.StartProgram(cmd, abs)
}

// ExternalDiff shows a file's change in the diff tool: gitswitch.difftool
// (a command with {path} {left} {right}), or git's own `git difftool`
// through the repo's config. staged shows the staged change; otherwise the
// one not staged yet.
func (s *Service) ExternalDiff(gitDir, rel string, staged bool) error {
	return s.externalDiff(gitDir, rel, staged, "")
}

// ExternalDiffAt shows what a commit (a hash) changed in one file.
func (s *Service) ExternalDiffAt(gitDir, rel, rev string) error {
	if !hashRe.MatchString(rev) {
		return errors.New("name the commit by its hash")
	}
	return s.externalDiff(gitDir, rel, false, rev)
}

var hashRe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

func (s *Service) externalDiff(gitDir, rel string, staged bool, rev string) error {
	if s.Tools == nil {
		return errors.New("external tools are not available")
	}
	wt, clean, abs, err := s.fileIn(gitDir, rel, false)
	if err != nil {
		return err
	}
	tpl := s.Lists.Load().DiffTool
	if tpl == "" {
		if s.Tools.ConfigGet(wt, "diff.tool") == "" {
			return errors.New("no diff tool: set one with `gitswitch set difftool <command>` (e.g. meld {left} {right}) or in git: git config --global diff.tool <name>")
		}
		args := []string{"difftool", "--no-prompt"}
		if rev != "" {
			return s.Tools.StartGit(wt, append(args, rev+"^", rev, "--", clean))
		}
		if staged {
			args = append(args, "--staged")
		}
		return s.Tools.StartGit(wt, append(args, "--", clean))
	}
	words, err := process.SplitCommand(tpl)
	if err != nil || len(words) == 0 {
		return errors.Join(err, errors.New("the diff tool command is empty"))
	}
	if !process.Uses(words, "left") && !process.Uses(words, "right") && !process.Uses(words, "path") {
		return errors.New("the diff tool command needs {left} and {right} (or {path}): gitswitch set difftool <command>")
	}
	tmp, err := os.MkdirTemp("", "gitswitch-diff-")
	if err != nil {
		return err
	}
	leftSpec, rightSpec := ":0:"+clean, ""
	if staged {
		leftSpec, rightSpec = "HEAD:"+clean, ":0:"+clean
	}
	if rev != "" {
		leftSpec, rightSpec = rev+"^:"+clean, rev+":"+clean
	}
	left, err := s.version(wt, tmp, "left", clean, leftSpec)
	if err == nil && leftSpec == ":0:"+clean {
		if b, ok := s.Tools.Show(wt, ":0:"+clean); !ok || b == nil { // not in the index: compare with HEAD
			left, err = s.version(wt, tmp, "left", clean, "HEAD:"+clean)
		}
	}
	right := abs
	if err == nil && rightSpec != "" {
		right, err = s.version(wt, tmp, "right", clean, rightSpec)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	vars := map[string]string{"path": abs, "left": left, "right": right}
	return process.Spawn(wt, words[0], process.Expand(words[1:], vars), func() { _ = os.RemoveAll(tmp) })
}

// ExternalMerge opens a conflicted file in the merge tool: gitswitch.mergetool
// (a command with {path} {merged} {base} {left} {right}, left = yours,
// right = theirs), or git's own `git mergetool` through the repo's config.
func (s *Service) ExternalMerge(gitDir, rel string) error {
	if s.Tools == nil {
		return errors.New("external tools are not available")
	}
	wt, clean, abs, err := s.fileIn(gitDir, rel, false)
	if err != nil {
		return err
	}
	tpl := s.Lists.Load().MergeTool
	if tpl == "" {
		if s.Tools.ConfigGet(wt, "merge.tool") == "" {
			return errors.New("no merge tool: set one with `gitswitch set mergetool <command>` (e.g. meld {left} {base} {right} --output {merged}) or in git: git config --global merge.tool <name>")
		}
		return s.Tools.StartGit(wt, []string{"mergetool", "--no-prompt", "--", clean})
	}
	words, err := process.SplitCommand(tpl)
	if err != nil || len(words) == 0 {
		return errors.Join(err, errors.New("the merge tool command is empty"))
	}
	if !process.Uses(words, "merged") && !process.Uses(words, "path") {
		return errors.New("the merge tool command needs {merged}: gitswitch set mergetool <command>")
	}
	tmp, err := os.MkdirTemp("", "gitswitch-merge-")
	if err != nil {
		return err
	}
	vars := map[string]string{"path": abs, "merged": abs}
	for name, stage := range map[string]string{"base": ":1:", "left": ":2:", "right": ":3:"} {
		if vars[name], err = s.version(wt, tmp, name, clean, stage+clean); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	return process.Spawn(wt, words[0], process.Expand(words[1:], vars), func() { _ = os.RemoveAll(tmp) })
}

// version writes one version of a file (empty when it does not exist) into
// dir/<role>/<file name>, so the tool shows the right name and extension.
func (s *Service) version(wt, dir, role, clean, spec string) (string, error) {
	d := filepath.Join(dir, role)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(d, filepath.Base(filepath.FromSlash(clean)))
	b, _ := s.Tools.Show(wt, spec)
	return p, os.WriteFile(p, b, 0o600)
}
