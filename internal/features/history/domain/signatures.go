package domain

import "strings"

// MaxSignatureHashes is how many commits one signature lookup may ask
// about: verifying runs gpg or ssh-keygen once per commit.
const MaxSignatureHashes = 200

// Signature is what git says about a commit's signature (git log %G?).
type Signature struct {
	// Status is git's letter: G good, B bad, U good but of unknown
	// validity, X good but expired, Y made with an expired key, R made
	// with a revoked key, E cannot be checked (no key or no allowed
	// signers file), N not signed.
	Status   string `json:"status"`
	Label    string `json:"label"`    // the status in plain words
	Verified bool   `json:"verified"` // G: show a "verified" badge
	Signer   string `json:"signer"`   // who signed (gpg user id, or the ssh principal)
	Key      string `json:"key"`      // the key or fingerprint used
}

// SignatureLabel explains a status letter in plain words.
func SignatureLabel(status string) string {
	switch status {
	case "G":
		return "Verified signature"
	case "B":
		return "Bad signature: the commit does not match it"
	case "U":
		return "Good signature, but the key is not trusted"
	case "X":
		return "Good signature, but it has expired"
	case "Y":
		return "Signed with a key that has expired"
	case "R":
		return "Signed with a key that was revoked"
	case "E":
		return "Signed, but it cannot be checked here (key or allowed signers file missing)"
	}
	return "Not signed"
}

// SignatureReader is the optional part of Log that reads signatures. The
// use case looks for it at run time, so other Logs need nothing.
type SignatureReader interface {
	// Signatures verifies the commits' signatures. A hash git does not
	// know is left out of the result.
	Signatures(gitDir string, hashes []string) (map[string]Signature, error)
}

// CleanSignatureHashes keeps the plausible commit hashes of a request:
// hex, 4 to 64 characters, each once, at most MaxSignatureHashes.
func CleanSignatureHashes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range in {
		h = strings.ToLower(strings.TrimSpace(h))
		if len(h) < 4 || len(h) > 64 || seen[h] || strings.Trim(h, "0123456789abcdef") != "" {
			continue
		}
		seen[h] = true
		out = append(out, h)
		if len(out) == MaxSignatureHashes {
			break
		}
	}
	return out
}

// ParseSignatures reads `git log --format=%H%x1f%G?%x1f%GS%x1f%GK%x1e`.
func ParseSignatures(out string) map[string]Signature {
	m := map[string]Signature{}
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.Trim(rec, "\r\n"), "\x1f")
		if len(f) < 4 || f[0] == "" {
			continue
		}
		st := strings.TrimSpace(f[1])
		if st == "" {
			st = "N"
		}
		m[f[0]] = Signature{Status: st, Label: SignatureLabel(st), Verified: st == "G",
			Signer: strings.TrimSpace(f[2]), Key: strings.TrimSpace(f[3])}
	}
	return m
}
