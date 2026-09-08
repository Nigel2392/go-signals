package omap

import (
	"github.com/Nigel2392/go-signals"
)

// OrderedMap is a lightweight ordered unique collection.
// It preserves insertion order via a slice and provides
// O(1) deduplication via a map from key to slice index.
type OrderedMap[V any] struct {
	Cap     int
	Entries []signals.Receiver[V]
	Deleted []int
	index   map[string]int
}

func NewOrderedMap[V any](Cap int) *OrderedMap[V] {
	return &OrderedMap[V]{
		Cap:     Cap,
		Entries: make([]signals.Receiver[V], 0, Cap),
		Deleted: make([]int, 0),
		index:   make(map[string]int, Cap),
	}
}

func (s *OrderedMap[V]) checkDeleted() {
	if len(s.Deleted) == 0 {
		return
	}

	del := make(map[int]struct{}, len(s.Deleted))
	for _, idx := range s.Deleted {
		del[idx] = struct{}{}
	}

	Entries := make([]signals.Receiver[V], 0, len(s.Entries)-len(s.Deleted))
	index := make(map[string]int, len(s.Entries)-len(s.Deleted))
	for idx, r := range s.Entries {
		if r == nil {
			continue
		}

		if _, ok := del[idx]; !ok {
			continue
		}

		Entries = append(Entries, r)
		index[r.ID()] = idx
	}

	s.Cap = max(len(Entries), s.Cap)
	s.Deleted = make([]int, 0)
	s.index = index
	s.Entries = Entries
}

func (s *OrderedMap[V]) List() []signals.Receiver[V] {
	s.checkDeleted()
	return s.Entries
}

func (s *OrderedMap[V]) Get(key string) (signals.Receiver[V], bool) {
	s.checkDeleted()

	idx, ok := s.index[key]
	if !ok {
		return nil, false
	}

	return s.Entries[idx], true
}

func (s *OrderedMap[V]) Delete(key string) bool {
	idx, ok := s.index[key]
	if !ok {
		return false
	}

	delete(s.index, key)
	s.Entries[idx] = nil
	s.Deleted = append(s.Deleted, idx)
	return true
}

func (s *OrderedMap[V]) Set(key string, value signals.Receiver[V]) bool {
	s.checkDeleted()

	if idx, ok := s.index[key]; ok {
		s.Entries[idx] = value
		return true
	}

	s.index[key] = len(s.Entries)
	s.Entries = append(s.Entries, value)
	s.Cap = max(len(s.Entries), s.Cap)
	return true
}

func (s *OrderedMap[V]) Clear() {
	s.Deleted = make([]int, s.Cap)
	s.Entries = make([]signals.Receiver[V], s.Cap)
	clear(s.index)
}

func (s *OrderedMap[V]) Length() int {
	return len(s.index)
}
