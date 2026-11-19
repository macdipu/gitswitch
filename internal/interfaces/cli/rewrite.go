package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
	rewrite "github.com/macdipu/gitswitch/internal/features/rewrite/domain"
)

const (
	rebaseIUsage   = "gitswitch rebase -i [--since <commit>] [--todo <file>] [--messages <file.json>] [--plan] [-y]"
	rewordUsage    = "gitswitch reword <commit> [-m <message>] [-y]"
	fixupIntoUsage = "gitswitch fixup-into <commit> [-y]"
)

// rwFlag reads "--name value" or "--name=value" at args[i]; n is how
// many more arguments it took.
func rwFlag(args []string, i int, name string) (value string, n int, ok bool) {
	a := args[i]
	if a == name && i+1 < len(args) {
		return args[i+1], 1, true
	}
	if v, found := strings.CutPrefix(a, name+"="); found {
		return v, 0, true
	}
	return "", 0, false
}

// cmdRebaseInteractive rewrites the commits that are not pushed (or those
// since a commit): reorder, squash, fixup, reword, drop. In a terminal the
// todo opens in your editor, in git's own format; with --todo it is read
// from a file, and the messages rewords need come from --messages (a JSON
// object of commit: message) or, in a terminal, from your editor. The
// plan is shown and confirmed before anything changes; a clash stops the
// rebase for gitswitch conflict, continue, skip or abort.
func (x *CLI) cmdRebaseInteractive(args []string) error {
	var since, todoFile, msgFile string
	var yes, plan bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		if v, n, ok := rwFlag(args, i, "--since"); ok {
			since, i = v, i+n
		} else if v, n, ok := rwFlag(args, i, "--todo"); ok {
			todoFile, i = v, i+n
		} else if v, n, ok := rwFlag(args, i, "--messages"); ok {
			msgFile, i = v, i+n
		} else {
			switch a {
			case "-i", "--interactive":
			case "-y", "--yes":
				yes = true
			case "--plan":
				plan = true
			default:
				return usageErr(rebaseIUsage)
			}
		}
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	t, err := x.app.Rewrite.Todo(gd, since)
	if err != nil {
		return err
	}
	if t.Pending {
		return errors.New("a rewrite is stopped in this repo: finish it with gitswitch continue, skip or abort first")
	}
	if t.Blocked != "" {
		return errors.New(t.Blocked)
	}
	if len(t.Commits) == 0 {
		x.say("There is nothing to rewrite: no commit that is not pushed yet.")
		if t.Note != "" {
			x.say(t.Note)
		}
		return nil
	}
	if t.Note != "" {
		x.say(t.Note)
	}
	var steps []rewrite.Step
	switch {
	case todoFile != "":
		b, err := os.ReadFile(todoFile)
		if err != nil {
			return err
		}
		if steps, err = rewrite.ParseTodo(string(b), t.Commits); err != nil {
			return err
		}
	case interactive():
		text, err := editText(rewrite.FormatTodo(t.Commits), "gitswitch-rebase-todo")
		if err != nil {
			return err
		}
		if steps, err = rewrite.ParseTodo(text, t.Commits); err != nil {
			return err
		}
	default:
		return errors.New("not in a terminal: give the todo with --todo <file> (lines like `pick <commit>`, `reword <commit>`, `squash <commit>`, `fixup <commit>`, `drop <commit>`)")
	}
	if len(steps) == 0 {
		x.say("Cancelled: nothing changed.")
		return nil
	}
	if msgFile != "" {
		if steps, err = applyMessages(msgFile, t.Commits, steps); err != nil {
			return err
		}
	}
	steps, prompts := rewrite.Prepare(t.Commits, steps)
	short := map[string]string{}
	for _, c := range t.Commits {
		short[c.Hash] = c.Short
	}
	for _, p := range prompts {
		if !interactive() {
			if p.Combine {
				continue // the default: the messages joined
			}
			return fmt.Errorf("reword %s needs a message: add it to the --messages file ({\"%s\": \"new message\"})", short[steps[p.Index].Hash], short[steps[p.Index].Hash])
		}
		what := "the new message for " + short[steps[p.Index].Hash] + " " + p.Subject
		if p.Combine {
			what = "the message for the squashed commit that starts with " + p.Subject
		}
		text, err := editText(p.Initial+"\n\n# Write "+what+" above.\n# Lines starting with # are ignored. An empty message cancels.\n", "gitswitch-message")
		if err != nil {
			return err
		}
		msg := rewrite.StripComments(text)
		if msg == "" {
			if p.Combine {
				continue
			}
			x.say("Cancelled: nothing changed.")
			return nil
		}
		steps[p.Index].Message = msg
	}
	p, _, err := x.app.Rewrite.Preview(gd, since, steps)
	if err != nil {
		return err
	}
	x.printRewritePlan(t, p)
	if plan {
		return nil
	}
	if p.NoChange {
		x.say("\nNothing would change.")
		return nil
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Rewrite these commits? (Undo brings the old ones back while they are not pushed)"); err != nil {
			return err
		}
	}
	r, err := x.app.Rewrite.Run(gd, since, steps)
	if err != nil || x.app.DryRun() {
		return err
	}
	return x.sayRewritten(r)
}

// applyMessages sets steps' messages from a JSON file {commit: message};
// a commit may be abbreviated.
func applyMessages(path string, commits []rewrite.Commit, steps []rewrite.Step) ([]rewrite.Step, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: expected a JSON object of commit: message (%v)", path, err)
	}
	out := append([]rewrite.Step(nil), steps...)
	for k, msg := range m {
		matched := false
		for _, c := range commits {
			if len(k) >= 4 && strings.HasPrefix(c.Hash, strings.ToLower(k)) {
				for i := range out {
					if out[i].Hash == c.Hash {
						out[i].Message, matched = msg, true
					}
				}
			}
		}
		if !matched {
			return nil, fmt.Errorf("%s: %q is not one of the commits to rewrite", path, k)
		}
	}
	return out, nil
}

func (x *CLI) printRewritePlan(t rewrite.Todo, p rewrite.Plan) {
	x.sayf("\nBefore (%d, oldest first):\n", len(t.Commits))
	for _, c := range t.Commits {
		x.sayf("  %s %s\n", c.Short, history.CleanText(c.Subject))
	}
	by := map[string]rewrite.Commit{}
	for _, c := range t.Commits {
		by[c.Hash] = c
	}
	x.sayf("\nAfter (%d):\n", p.After)
	for _, l := range p.SummaryLines(by) {
		x.say("  " + history.CleanText(l))
	}
	x.say("\n" + p.Summary() + ".")
}

func (x *CLI) sayRewritten(r rewrite.Rewritten) error {
	x.say(r.Message)
	if r.Outcome == rewrite.Stopped {
		x.say()
		for _, f := range r.Conflicts {
			x.say("  conflict  " + f)
		}
		return fmt.Errorf("the rewrite stopped: %d file(s) have conflicts", len(r.Conflicts))
	}
	return nil
}

// cmdReword gives an older, not-pushed commit a new message.
func (x *CLI) cmdReword(args []string) error {
	var rev, message string
	var haveMsg, yes bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message":
			if i+1 >= len(args) {
				return usageErr(rewordUsage)
			}
			message, haveMsg, i = args[i+1], true, i+1
		case strings.HasPrefix(a, "--message="):
			message, haveMsg = strings.TrimPrefix(a, "--message="), true
		case a == "-y" || a == "--yes":
			yes = true
		case rev == "" && !strings.HasPrefix(a, "-"):
			rev = a
		default:
			return usageErr(rewordUsage)
		}
	}
	if rev == "" {
		return usageErr(rewordUsage)
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	t, err := x.app.Rewrite.Todo(gd, rev)
	if err != nil {
		return err
	}
	if t.Blocked != "" {
		return errors.New(t.Blocked)
	}
	c := t.Commits[0]
	if !haveMsg {
		if !interactive() {
			return errors.New("give the new message with -m <message>")
		}
		text, err := editText(c.Message+"\n\n# Write the new message for "+c.Short+" above.\n# Lines starting with # are ignored. An empty message cancels.\n", "gitswitch-message")
		if err != nil {
			return err
		}
		if message = rewrite.StripComments(text); message == "" {
			x.say("Cancelled: nothing changed.")
			return nil
		}
	}
	x.sayf("Reword %s %s\n  to: %s\n", c.Short, history.CleanText(c.Subject), history.CleanText(rwFirstLine(message)))
	if n := len(t.Commits) - 1; n > 0 {
		x.sayf("The %d commit(s) after it are replayed on top (new hashes, same content).\n", n)
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Reword it?"); err != nil {
			return err
		}
	}
	r, err := x.app.Rewrite.Reword(gd, rev, message)
	if err != nil || x.app.DryRun() {
		return err
	}
	return x.sayRewritten(r)
}

func rwFirstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// cmdFixupInto folds the staged changes into an older, not-pushed commit.
func (x *CLI) cmdFixupInto(args []string) error {
	rev, yes, err := commitArg(args, fixupIntoUsage)
	if err != nil {
		return err
	}
	gd, err := x.requireRepo()
	if err != nil {
		return err
	}
	t, err := x.app.Rewrite.Todo(gd, rev)
	if err != nil {
		return err
	}
	if t.Blocked != "" && !strings.Contains(t.Blocked, "uncommitted changes") {
		return errors.New(t.Blocked)
	}
	c := t.Commits[0]
	x.sayf("The staged changes will be committed as a fixup of %s %s and folded into it.\n", c.Short, history.CleanText(c.Subject))
	if n := len(t.Commits) - 1; n > 0 {
		x.sayf("The %d commit(s) after it are replayed on top (new hashes). Other changes must not be left unstaged.\n", n)
	}
	if !yes && !x.app.DryRun() {
		if err := x.ask("Fold the staged changes into it?"); err != nil {
			return err
		}
	}
	r, err := x.app.Rewrite.FixupInto(gd, rev)
	if err != nil || x.app.DryRun() {
		return err
	}
	return x.sayRewritten(r)
}

// editText opens text in the user's editor ($VISUAL, $EDITOR, else vi, or
// notepad on Windows) through the shell, as git does, and returns what was
// saved.
func editText(text, name string) (string, error) {
	f, err := os.CreateTemp("", name+"-*.txt")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	var cmd *exec.Cmd
	if sh, err := exec.LookPath("sh"); err == nil {
		cmd = exec.Command(sh, "-c", editor+` "$@"`, "sh", filepath.ToSlash(f.Name()))
	} else {
		cmd = exec.Command(editor, f.Name())
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("the editor %q failed: %v", editor, err)
	}
	b, err := os.ReadFile(f.Name())
	return string(b), err
}
