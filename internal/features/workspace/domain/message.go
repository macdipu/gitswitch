package domain

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The trailer keys GitSwitch adds to a commit message.
const (
	SignedOffBy  = "Signed-off-by"
	CoAuthoredBy = "Co-authored-by"
)

// RecentCount is how many recent messages the commit box offers.
const RecentCount = 10

// AuthorsScanned is how many recent commits co-author suggestions come
// from.
const AuthorsScanned = 500

// Person is a name and an email, as a commit or a trailer shows them.
type Person struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// String is "Name <email>".
func (p Person) String() string { return p.Name + " <" + p.Email + ">" }

var personRe = regexp.MustCompile(`^([^<>\n]+?)\s*<([^<>\s@]+@[^<>\s@]+)>$`)

// ParsePerson reads "Name <email>", as a Co-authored-by trailer takes it.
func ParsePerson(s string) (Person, error) {
	m := personRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return Person{}, fmt.Errorf("%q is not a name and an email: write it as Name <email>, e.g. Ada Lovelace <ada@example.com>", strings.TrimSpace(s))
	}
	return Person{Name: strings.TrimSpace(m[1]), Email: m[2]}, nil
}

// Trailer is one "Key: value" line at the end of a commit message.
type Trailer struct {
	Key   string `json:"key"`
	Value string `json:"value"` // may go on over indented lines
}

func (t Trailer) String() string { return t.Key + ": " + t.Value }

// same reports the same trailer: the key in any case, the value with any
// spacing and case.
func (t Trailer) same(o Trailer) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	return strings.EqualFold(t.Key, o.Key) && norm(t.Value) == norm(o.Value)
}

var trailerRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)[ \t]*:[ \t]*(\S.*)$`)

// ParseTrailers reads a paragraph as a trailer block: every line a
// "Key: value" or an indented line going on with the one before. ok is
// false when the paragraph is not one.
func ParseTrailers(paragraph string) (ts []Trailer, ok bool) {
	for _, l := range strings.Split(strings.TrimRight(paragraph, "\n"), "\n") {
		if m := trailerRe.FindStringSubmatch(l); m != nil {
			ts = append(ts, Trailer{Key: m[1], Value: strings.TrimRight(m[2], " \t")})
			continue
		}
		if len(ts) > 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && strings.TrimSpace(l) != "" {
			ts[len(ts)-1].Value += "\n" + strings.TrimRight(l, " \t")
			continue
		}
		return nil, false
	}
	return ts, len(ts) > 0
}

// SplitMessage cuts a message into its summary (the first line), its body
// and the trailer block at its end (the last paragraph after the
// summary, when every line of it is a trailer).
func SplitMessage(message string) (summary, body string, trailers []Trailer) {
	message = Message(message)
	summary, rest, _ := strings.Cut(message, "\n")
	summary, rest = strings.TrimSpace(summary), strings.TrimSpace(rest)
	last := rest
	if i := strings.LastIndex(rest, "\n\n"); i >= 0 {
		last = rest[i+2:]
	}
	if ts, ok := ParseTrailers(last); ok {
		return summary, strings.TrimSpace(strings.TrimSuffix(rest, last)), ts
	}
	return summary, rest, nil
}

// BuildMessage puts a commit message together: the summary, a blank line
// and the description, then a blank line and the trailer block. Trailers
// already at the end of the description stay, in front of the added ones,
// and no trailer appears twice. A summary with more lines starts the
// description with the rest. With no summary and no description it is ""
// whatever the trailers: they are not a message.
func BuildMessage(summary, description string, trailers []Trailer) string {
	full := Message(Message(summary) + "\n\n" + Message(description))
	if full == "" {
		return ""
	}
	s, body, have := SplitMessage(full)
	var all []Trailer
	for _, t := range append(have, trailers...) {
		t.Key, t.Value = strings.TrimSpace(t.Key), strings.TrimSpace(t.Value)
		if t.Key == "" || t.Value == "" || containsTrailer(all, t) {
			continue
		}
		all = append(all, t)
	}
	out := s
	if body != "" {
		out += "\n\n" + body
	}
	if len(all) > 0 {
		lines := make([]string, len(all))
		for i, t := range all {
			lines[i] = t.String()
		}
		out += "\n\n" + strings.Join(lines, "\n")
	}
	return out
}

// WithTrailers adds trailers to a whole message (see BuildMessage).
func WithTrailers(message string, trailers []Trailer) string {
	return BuildMessage(message, "", trailers)
}

func containsTrailer(ts []Trailer, t Trailer) bool {
	for _, o := range ts {
		if o.same(t) {
			return true
		}
	}
	return false
}

// CommitOptions are the extras of one commit or amend.
type CommitOptions struct {
	// Signoff adds "Signed-off-by: <the repo's name and email>"; nil
	// follows the gitswitch.signoff setting.
	Signoff *bool
	// CoAuthors are "Name <email>", each added as Co-authored-by.
	CoAuthors []string
}

// Trailers are the Co-authored-by trailers for coAuthors, then with
// signoff the Signed-off-by one for me. It refuses a co-author not written
// as "Name <email>", and one with my own email: the commit is already
// mine.
func Trailers(me Person, signoff bool, coAuthors []string) ([]Trailer, error) {
	var ts []Trailer
	for _, c := range coAuthors {
		if strings.TrimSpace(c) == "" {
			continue
		}
		p, err := ParsePerson(c)
		if err != nil {
			return nil, err
		}
		if me.Email != "" && strings.EqualFold(p.Email, me.Email) {
			return nil, fmt.Errorf("%s is you: the commit is already yours, so leave it out of the co-authors", p)
		}
		ts = append(ts, Trailer{Key: CoAuthoredBy, Value: p.String()})
	}
	if signoff {
		if strings.TrimSpace(me.Email) == "" || strings.TrimSpace(me.Name) == "" {
			return nil, errors.New("signing off needs this repo's name and email: set them with gitswitch use <account>, or git config user.name and user.email")
		}
		ts = append(ts, Trailer{Key: SignedOffBy, Value: me.String()})
	}
	return ts, nil
}

// Recent is a commit message written earlier, to write the next one from.
type Recent struct {
	Subject string    `json:"subject"`
	Message string    `json:"message"` // the whole message, without Signed-off-by and Co-authored-by
	Time    time.Time `json:"time"`
}

// RecentMessages keeps the newest n distinct messages, newest first.
// Signed-off-by and Co-authored-by trailers are taken off, since the
// commit box adds them again; empty messages go.
func RecentMessages(list []Recent, n int) []Recent {
	sorted := append([]Recent(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.After(sorted[j].Time) })
	out, seen := []Recent{}, map[string]bool{}
	for _, r := range sorted {
		if len(out) >= n {
			break
		}
		s, body, ts := SplitMessage(r.Message)
		var keep []Trailer
		for _, t := range ts {
			if !strings.EqualFold(t.Key, SignedOffBy) && !strings.EqualFold(t.Key, CoAuthoredBy) {
				keep = append(keep, t)
			}
		}
		m := BuildMessage(s, body, keep)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, Recent{Subject: s, Message: m, Time: r.Time})
	}
	return out
}

// CoAuthor is someone who made commits in the repo, suggested as a
// co-author.
type CoAuthor struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Commits int    `json:"commits"` // among the recent commits read
}

// CoAuthors counts the authors of recent commits (newest first) by email,
// most commits first (ties: the most recent first), each with the name of
// their newest commit. Emails in exclude (you, your other accounts) are
// left out.
func CoAuthors(authors []Person, exclude []string) []CoAuthor {
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[strings.ToLower(strings.TrimSpace(e))] = true
	}
	out, at := []CoAuthor{}, map[string]int{}
	for _, a := range authors {
		key := strings.ToLower(strings.TrimSpace(a.Email))
		if key == "" || skip[key] || strings.TrimSpace(a.Name) == "" {
			continue
		}
		if i, ok := at[key]; ok {
			out[i].Commits++
			continue
		}
		at[key] = len(out)
		out = append(out, CoAuthor{Name: strings.TrimSpace(a.Name), Email: strings.TrimSpace(a.Email), Commits: 1})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Commits > out[j].Commits })
	return out
}

// ParseOnOff reads a saved or typed on/off value as git's "true" or
// "false"; ok is false for anything else.
func ParseOnOff(s string) (v string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "on", "yes", "1":
		return "true", true
	case "false", "off", "no", "0":
		return "false", true
	}
	return "", false
}

// SignoffSetting says whether commits get a Signed-off-by trailer
// (gitswitch.signoff): the repo's own choice, else the one for every
// repo, else off.
type SignoffSetting struct {
	Mode   bool   `json:"mode"`   // in effect
	Repo   string `json:"repo"`   // "on" or "off" for this repo; "" follows Global
	Global bool   `json:"global"` // for every repo
}

// NewSignoffSetting reads the saved values; an unknown one counts as
// unset.
func NewSignoffSetting(global, repo string) SignoffSetting {
	var s SignoffSetting
	if v, ok := ParseOnOff(global); ok {
		s.Global = v == "true"
	}
	s.Mode = s.Global
	if v, ok := ParseOnOff(repo); ok {
		s.Mode = v == "true"
		s.Repo = map[bool]string{true: "on", false: "off"}[s.Mode]
	}
	return s
}

// Describe is e.g. "on (this repo)" or "off (every repo)".
func (s SignoffSetting) Describe() string {
	v := map[bool]string{true: "on", false: "off"}[s.Mode]
	if s.Repo != "" {
		return v + " (this repo)"
	}
	return v + " (every repo)"
}

// CommitInfo is what the commit box offers in a repo.
type CommitInfo struct {
	Recent         []Recent       `json:"recent"`
	Draft          string         `json:"draft"` // the message of the last commit that failed; "" for none
	Signoff        bool           `json:"signoff"`
	SignoffSetting SignoffSetting `json:"signoffSetting"`
	CoAuthors      []CoAuthor     `json:"coauthors"`
	Identity       Person         `json:"identity"` // the name and email commits and sign-offs show
}

// Messages reads earlier commit messages and authors, keeps the message
// of a commit that failed, and the sign-off setting.
type Messages interface {
	// Recent lists up to max messages of commits authored with email, on
	// every branch, newest first.
	Recent(gitDir, email string, max int) ([]Recent, error)
	// Authors lists the authors of up to max recent commits from HEAD,
	// newest first.
	Authors(gitDir string, max int) ([]Person, error)
	// Identity is the name and email git commits with in the repo.
	Identity(gitDir string) Person
	// Draft is the kept message ("" for none); SaveDraft keeps one, and
	// "" removes it.
	Draft(gitDir string) string
	SaveDraft(gitDir, message string) error
	// SignoffValues reads gitswitch.signoff for every repo and the repo's
	// own (gitDir "": only the global one); SaveSignoff sets it, value ""
	// removes it.
	SignoffValues(gitDir string) (global, repo string)
	SaveSignoff(gitDir, value string) error
}
