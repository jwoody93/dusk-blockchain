package agreement

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type storedAgreements []Agreement

func (s storedAgreements) Len() int {
	return len(s)
}

func (s storedAgreements) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s storedAgreements) Less(i, j int) bool {
	return s[i].intRepr.Cmp(s[j].intRepr) <= 0
}

func (s storedAgreements) String() string {
	var sb strings.Builder
	for i, aggro := range s {
		if i == 0 {
			sb.WriteString("[\n")
		}
		sb.WriteString("\t")
		sb.WriteString(hex.EncodeToString(aggro.signedVotes))
		sb.WriteString(fmt.Sprintf(" round: %d step: %d sender: %s", aggro.Round, aggro.Step, hex.EncodeToString(aggro.Header.Sender())))
		sb.WriteString("\n")
	}
	sb.WriteString("]")
	return sb.String()
}

type store struct {
	sync.RWMutex
	collected map[uint8]storedAgreements
}

func newStore() *store {
	return &store{
		collected: make(map[uint8]storedAgreements),
	}
}

func (s *store) String() string {
	var sb strings.Builder
	for k, v := range s.collected {
		sb.WriteString(strconv.Itoa(int(k)))
		sb.WriteString(": ")
		sb.WriteString(v.String())
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *store) Size() int {
	var i int
	for _, v := range s.collected {
		i += len(v)
	}
	return i
}

// Put collects the Agreement and returns the number of agreement stored for a blockhash
func (s *store) Insert(a Agreement, weight int) int {
	s.Lock()
	defer s.Unlock()

	idx := s.find(a)
	if idx == -1 {
		agreements := make([]Agreement, weight)
		for i := range agreements {
			agreements[i] = a
		}

		s.collected[a.Step] = storedAgreements(agreements)
		return weight
	}

	stored := s.collected[a.Step]
	// if the Agreement is already in the store we do not add it
	if s.contains(idx, a) {
		return len(stored)
	}

	// efficient insertion with minimal element copy and no additional allocation
	// github.com/golang.go/wiki/SliceTricks
	for i := 0; i < weight; i++ {
		stored = append(stored, Agreement{})
		copy(stored[idx+1:], stored[idx:])
		stored[idx] = a
	}

	s.collected[a.Step] = stored
	return len(stored)
}

func (s *store) Get(step uint8) []Agreement {
	s.RLock()
	defer s.RUnlock()
	return s.collected[step]
}

func (s *store) Find(a Agreement) int {
	s.RLock()
	defer s.RUnlock()
	return s.find(a)
}

// Find returns the index of an Agreement in the stored collection or, if the Agreement has not been stored, the index at which it would be stored.
// In case no Agreement is stored for the blockHash specified, it returns -1
func (s *store) find(a Agreement) int {
	stored := s.collected[a.Step]
	if stored == nil {
		return -1
	}

	return sort.Search(len(stored), func(i int) bool {
		return stored[i].Cmp(a) <= 0
	})
}

func (s *store) Contains(a Agreement) bool {
	s.RLock()
	defer s.RUnlock()
	idx := s.find(a)
	return s.contains(idx, a)
}

func (s *store) contains(idx int, a Agreement) bool {
	stored := s.collected[a.Step]
	if idx == -1 {
		return false
	}

	if idx < len(stored) && stored[idx].Equal(a) {
		return true
	}

	return false
}

func (s *store) Clear() {
	s.Lock()
	defer s.Unlock()
	for k := range s.collected {
		delete(s.collected, k)
	}
}
