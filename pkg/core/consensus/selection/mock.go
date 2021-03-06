package selection

import (
	"bytes"

	crypto "github.com/dusk-network/dusk-crypto/hash"
)

// MockSelectionEventBuffer mocks a Selection event, marshals it, and returns the
// resulting buffer.
func MockSelectionEventBuffer(hash []byte) *bytes.Buffer {
	se := MockSelectionEvent(hash)
	r := new(bytes.Buffer)
	_ = MarshalScore(r, se)
	return r
}

// MockSelectionEvent mocks a Selection event and returns it.
func MockSelectionEvent(hash []byte) *Score {
	score, _ := crypto.RandEntropy(32)
	proof, _ := crypto.RandEntropy(1477)
	z, _ := crypto.RandEntropy(32)
	subset, _ := crypto.RandEntropy(32)
	seed, _ := crypto.RandEntropy(33)
	prevHash, _ := crypto.RandEntropy(32)

	return &Score{
		Score:         score,
		Proof:         proof,
		Z:             z,
		Seed:          seed,
		BidListSubset: subset,
		PrevHash:      prevHash,
		VoteHash:      hash,
	}
}
