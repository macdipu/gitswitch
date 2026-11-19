package application

import (
	"errors"

	history "github.com/macdipu/gitswitch/internal/features/history/domain"
)

// Signatures says, for up to history.MaxSignatureHashes commits (by full
// or short hash), whether each is signed and whether the signature checks
// out. The result is keyed by the hash as it was asked, in lower case;
// commits git does not know are left out. Local verification needs the
// repo to know the signer's key: ssh signatures use the allowed signers
// file gitswitch writes for an account that signs.
func (s *Service) Signatures(gitDir string, hashes []string) (map[string]history.Signature, error) {
	r, ok := s.Log.(history.SignatureReader)
	if !ok {
		return nil, errors.New("signatures cannot be read here")
	}
	hashes = history.CleanSignatureHashes(hashes)
	if len(hashes) == 0 {
		return map[string]history.Signature{}, nil
	}
	return r.Signatures(gitDir, hashes)
}
