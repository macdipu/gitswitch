package infrastructure

import (
	"bytes"
	"os/exec"
	"strings"

	gitinfra "github.com/macdipu/gitswitch/internal/features/git/infrastructure"
	"github.com/macdipu/gitswitch/internal/platform/process"
)

// Tools implements the repository domain's Tools.
type Tools struct{}

func (Tools) Show(wt, spec string) ([]byte, bool) {
	cmd := exec.Command(gitinfra.Program(), "-C", wt, "cat-file", "blob", spec)
	var out bytes.Buffer
	cmd.Stdout = &out
	process.HideWindow(cmd)
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func (Tools) ConfigGet(wt, key string) string {
	cmd := exec.Command(gitinfra.Program(), "-C", wt, "config", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	process.HideWindow(cmd)
	if cmd.Run() != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func (Tools) StartGit(wt string, args []string) error {
	return process.Spawn(wt, gitinfra.Program(), append([]string{"-C", wt}, args...), nil)
}
