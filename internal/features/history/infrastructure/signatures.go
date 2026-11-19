package infrastructure

import (
	"strings"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// Signatures implements history.SignatureReader with
// git log --no-walk --format=%H %G? %GS %GK. The result is keyed by the
// hash as it was asked (short hashes too).
func (l *Log) Signatures(gitDir string, hashes []string) (map[string]history.Signature, error) {
	read := func(hs []string) (map[string]history.Signature, error) {
		args := []string{"--git-dir=" + gitDir, "log", "--no-walk=unsorted", "--format=%H%x1f%G?%x1f%GS%x1f%GK%x1e", "--end-of-options"}
		out, err := l.Git.Run(append(args, hs...)...)
		if err != nil {
			return nil, err
		}
		m := history.ParseSignatures(out)
		l.markUncheckable(gitDir, m)
		return m, nil
	}
	full, err := read(hashes)
	if err != nil {
		// One unknown hash fails the whole lookup: ask one by one and
		// leave out what git does not know.
		full = map[string]history.Signature{}
		for _, h := range hashes {
			if m, err := read([]string{h}); err == nil {
				for k, v := range m {
					full[k] = v
				}
			}
		}
	}
	out := map[string]history.Signature{}
	for _, h := range hashes {
		for k, v := range full {
			if k == h || strings.HasPrefix(k, h) {
				out[h] = v
				break
			}
		}
	}
	return out, nil
}

// markUncheckable turns N into E for commits that do carry a signature.
// Without the signer's key (no allowed signers file for ssh) git says N
// for them too, as if they were not signed; the raw commit shows the
// gpgsig header.
func (l *Log) markUncheckable(gitDir string, m map[string]history.Signature) {
	var none []string
	for h, sig := range m {
		if sig.Status == "N" {
			none = append(none, h)
		}
	}
	if len(none) == 0 {
		return
	}
	args := []string{"--git-dir=" + gitDir, "log", "--no-walk=unsorted", "--format=raw", "--end-of-options"}
	out, err := l.Git.Run(append(args, none...)...)
	if err != nil {
		return
	}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		if h, ok := strings.CutPrefix(line, "commit "); ok {
			cur = strings.Fields(h + " ")[0]
		} else if strings.HasPrefix(line, "gpgsig") && cur != "" {
			if sig, ok := m[cur]; ok && sig.Status == "N" {
				sig.Status = "E"
				sig.Label = history.SignatureLabel("E")
				m[cur] = sig
			}
		} else if line == "" {
			cur = "" // the message follows: no more headers
		}
	}
}
