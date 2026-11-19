package domain

import (
	"fmt"
	"strings"
)

// FormatTodo is the todo a person edits in a terminal, in git's own
// format: one `pick <short> <subject>` line per commit, oldest first, and
// the instructions as comments.
func FormatTodo(commits []Commit) string {
	var b strings.Builder
	for _, c := range commits {
		fmt.Fprintf(&b, "pick %s %s\n", c.Short, c.Subject)
	}
	b.WriteString(`
# Rewrite the history: edit the lines above, then save and close.
#
# Commands:
# p, pick <commit>   = keep the commit as it is
# r, reword <commit> = keep it, with a new message (you are asked next)
# s, squash <commit> = fold it into the commit before, joining the messages
# f, fixup <commit>  = fold it into the commit before, dropping its message
# d, drop <commit>   = remove the commit
#
# The lines are the new history, oldest first: move a line to move a commit.
# Every commit must stay in the list (drop it to remove it).
# Delete all the lines (or leave only comments) to cancel.
`)
	return b.String()
}

// ParseTodo reads a todo in git's format (blank lines and lines starting
// with # are ignored; text after the commit is a subject and is ignored;
// the hashes may be abbreviated) into steps. No lines at all is an empty
// list, which means cancel.
func ParseTodo(text string, commits []Commit) ([]Step, error) {
	var steps []Step
	for n, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return nil, fmt.Errorf("todo line %d (%q): expected `<action> <commit>`", n+1, line)
		}
		a, err := ParseAction(f[0])
		if err != nil {
			return nil, fmt.Errorf("todo line %d: %v", n+1, err)
		}
		steps = append(steps, Step{Hash: f[1], Action: a})
	}
	if len(steps) == 0 {
		return nil, nil
	}
	return Resolve(commits, steps)
}

// Prompt asks for a message: the new message of a reword, or the combined
// message of a commit with squashed commits folded in.
type Prompt struct {
	Index   int    `json:"index"`   // the step whose Message is asked for
	Combine bool   `json:"combine"` // the message of a squash group, not one commit
	Initial string `json:"initial"` // the text to start from
	Subject string `json:"subject"` // the commit it is for
}

// Prepare finds the messages steps still need from a person: a reword with
// no message, and each squash group with no message on any step (its
// default is the commits' messages joined). Rewords inside a squash group
// are filled with the commit's own message, since the group's one message
// replaces them.
func Prepare(commits []Commit, steps []Step) ([]Step, []Prompt) {
	by := map[string]Commit{}
	for _, c := range commits {
		by[c.Hash] = c
	}
	out := append([]Step(nil), steps...)
	var prompts []Prompt
	for i := 0; i < len(out); i++ {
		if out[i].Action != Pick && out[i].Action != Reword {
			continue
		}
		last, hasSquash, given := -1, false, Tidy(out[i].Message) != "" && out[i].Action == Reword
		parts := []string{Tidy(by[out[i].Hash].Message)}
		j := i + 1
		for ; j < len(out) && (out[j].Action == Squash || out[j].Action == Fixup || out[j].Action == Drop); j++ {
			switch out[j].Action {
			case Squash:
				hasSquash, last = true, j
				parts = append(parts, Tidy(by[out[j].Hash].Message))
				given = given || Tidy(out[j].Message) != ""
			case Fixup:
				last = j
			}
		}
		head := out[i]
		switch {
		case head.Action == Reword && !hasSquash && Tidy(head.Message) == "":
			prompts = append(prompts, Prompt{Index: i, Initial: by[head.Hash].Message, Subject: by[head.Hash].Subject})
		case hasSquash && !given:
			if head.Action == Reword && Tidy(head.Message) == "" {
				out[i].Message = by[head.Hash].Message
			}
			prompts = append(prompts, Prompt{Index: last, Combine: true, Initial: strings.Join(nonEmpty(parts), "\n\n"), Subject: by[head.Hash].Subject})
		case hasSquash && head.Action == Reword && Tidy(head.Message) == "":
			out[i].Message = by[head.Hash].Message
		}
		i = j - 1
	}
	return out, prompts
}

func nonEmpty(l []string) []string {
	var out []string
	for _, s := range l {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// StripComments removes the lines starting with # (the instructions a
// message editor shows) and tidies what is left.
func StripComments(text string) string {
	var keep []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return Tidy(strings.Join(keep, "\n"))
}
