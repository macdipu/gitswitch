package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// IgnoreKind is what to ignore for a path: the path itself, every file
// with its extension, or the folder that holds it.
type IgnoreKind string

const (
	IgnoreFile   IgnoreKind = "file"   // the path itself: /dir/name
	IgnoreExt    IgnoreKind = "ext"    // its extension, anywhere: *.ext
	IgnoreFolder IgnoreKind = "folder" // its folder (the path itself when it is one): /dir/
)

// Where an ignore pattern is written: the repo's .gitignore (shared with
// everyone through commits) or .git/info/exclude (this clone only).
const (
	GitignoreName = ".gitignore"
	ExcludeName   = ".git/info/exclude"
)

// Ignored is the result of ignoring a file.
type Ignored struct {
	Path      string   `json:"path"`      // the repo-relative path asked about
	Kind      string   `json:"kind"`      // file, ext or folder
	Pattern   string   `json:"pattern"`   // the line in the file, escaped as git reads it
	Where     string   `json:"where"`     // ".gitignore" or ".git/info/exclude"
	File      string   `json:"file"`      // the full path of that file
	Added     bool     `json:"added"`     // false: the line was there already
	Ignored   bool     `json:"ignored"`   // git now ignores the path (git check-ignore)
	Tracked   []string `json:"tracked"`   // files git still tracks, which no pattern ignores
	Untracked []string `json:"untracked"` // files taken out of git (git rm --cached), kept in the folder
	Note      string   `json:"note"`      // what to know, in plain words; "" when nothing
}

// ParseIgnoreKind reads a kind as typed; "" is a file.
func ParseIgnoreKind(k string) (IgnoreKind, error) {
	switch IgnoreKind(strings.ToLower(strings.TrimSpace(k))) {
	case "", IgnoreFile:
		return IgnoreFile, nil
	case IgnoreExt, "extension":
		return IgnoreExt, nil
	case IgnoreFolder, "dir", "directory":
		return IgnoreFolder, nil
	}
	return "", fmt.Errorf("%q is not a kind to ignore: use file, ext or folder", k)
}

// IgnoreTarget is the repo-relative path a pattern ignores: p itself, or
// for a folder the folder holding p (p itself when isDir). For ext it is p.
func IgnoreTarget(p string, kind IgnoreKind, isDir bool) (string, error) {
	if kind != IgnoreFolder || isDir {
		return p, nil
	}
	d := path.Dir(p)
	if d == "." {
		return "", fmt.Errorf("%s is at the top of the repo, not in a folder: ignore the file itself, or name a folder", p)
	}
	return d, nil
}

// IgnorePattern builds the gitignore line for a cleaned repo-relative path
// (see CleanPath). A file or folder is anchored at the repo's root
// ("/dir/name", "/dir/"), so a file of the same name elsewhere is not
// ignored; an extension is ignored everywhere ("*.log"). The name is
// escaped, so it matches that name only.
func IgnorePattern(p string, kind IgnoreKind, isDir bool) (string, error) {
	if strings.ContainsAny(p, "\n\r") {
		return "", errors.New("a name with a line break can't be written to an ignore file")
	}
	switch kind {
	case IgnoreExt:
		ext := Extension(path.Base(p))
		if ext == "" {
			return "", fmt.Errorf("%s has no extension: ignore the file itself, or its folder", p)
		}
		return EscapeIgnore("*." + escapeGlob(ext)), nil
	case IgnoreFile, IgnoreFolder:
		t, err := IgnoreTarget(p, kind, isDir)
		if err != nil {
			return "", err
		}
		pat := "/" + escapeGlob(t)
		if kind == IgnoreFolder || isDir {
			pat += "/"
		}
		return EscapeIgnore(pat), nil
	}
	return "", fmt.Errorf("%q is not a kind to ignore: use file, ext or folder", string(kind))
}

// Extension is a file name's extension without the dot ("tar.gz" gives
// "gz"), or "" when it has none. A name that only starts with a dot
// (".env") has none: "*.env" would ignore other files too.
func Extension(name string) string {
	i := strings.LastIndex(name, ".")
	if i <= 0 || i == len(name)-1 {
		return ""
	}
	return name[i+1:]
}

// escapeGlob escapes the characters git reads as wildcards, and the
// backslash itself, so a pattern matches the name literally.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// EscapeIgnore makes a pattern safe as a line of a gitignore file: a
// leading # (a comment) or ! (a negation) is escaped, and so are trailing
// spaces, which git would drop. Leading spaces need nothing: git keeps
// them. Glob characters are left alone.
func EscapeIgnore(pat string) string {
	if strings.HasPrefix(pat, "#") || strings.HasPrefix(pat, "!") {
		pat = `\` + pat
	}
	body := strings.TrimRight(pat, " ")
	if n := len(pat) - len(body); n > 0 {
		pat = body + strings.Repeat(`\ `, n)
	}
	return pat
}

// HasIgnoreLine reports whether content (a gitignore file) has the line
// exactly, CRLF line ends allowed.
func HasIgnoreLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSuffix(l, "\r") == line {
			return true
		}
	}
	return false
}

// AddIgnoreLine returns content with line added on its own line, ending
// in a newline, and whether it was added (false: it was there already).
func AddIgnoreLine(content, line string) (string, bool) {
	if HasIgnoreLine(content, line) {
		return content, false
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line + "\n", true
}

// Ignorer edits ignore files and asks git about them. Paths are
// repo-relative, taken literally.
type Ignorer interface {
	// IgnoreFile is the full path of the repo's root .gitignore, or with
	// local of its info/exclude (shared by its worktrees).
	IgnoreFile(gitDir string, local bool) (string, error)
	// ReadIgnore reads an ignore file; one that does not exist is "".
	ReadIgnore(file string) (string, error)
	// WriteIgnore writes an ignore file, making its folder if needed.
	WriteIgnore(file, content string) error
	// IsDir reports whether path is a folder in the working folder.
	IsDir(gitDir, path string) bool
	// Tracked lists the files git tracks at path (under it, for a folder).
	Tracked(gitDir, path string) ([]string, error)
	// Ignored reports whether git ignores path (git check-ignore); a
	// tracked file never is.
	Ignored(gitDir, path string) (bool, error)
	// Untrack takes path out of git, under it too for a folder, keeping
	// the files (git rm --cached -r).
	Untrack(worktree, path string) error
}
