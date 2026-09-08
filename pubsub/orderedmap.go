package pubsub

import (
	"github.com/Nigel2392/go-signals"
)

// orderedMap is a lightweight ordered unique collection.
// It preserves insertion order via a slice and provides
// O(1) deduplication via a map from key to slice index.
type orderedMap[V any] struct {
	cap     int
	entries []signals.Receiver[V]
	deleted []int
	index   map[string]int
}

func newOrderedMap[V any](cap int) *orderedMap[V] {
	return &orderedMap[V]{
		cap:     cap,
		entries: make([]signals.Receiver[V], 0, cap),
		deleted: make([]int, 0),
		index:   make(map[string]int, cap),
	}
}

func (s *orderedMap[V]) checkDeleted() {
	if len(s.deleted) == 0 {
		return
	}

	del := make(map[int]struct{}, len(s.deleted))
	for _, idx := range s.deleted {
		del[idx] = struct{}{}
	}

	entries := make([]signals.Receiver[V], 0, len(s.entries)-len(s.deleted))
	index := make(map[string]int, len(s.entries)-len(s.deleted))
	for idx, r := range s.entries {
		if r == nil {
			continue
		}

		if _, ok := del[idx]; !ok {
			continue
		}

		entries = append(entries, r)
		index[r.ID()] = idx
	}

	s.cap = max(len(entries), s.cap)
	s.deleted = make([]int, 0)
	s.index = index
	s.entries = entries
}

func (s *orderedMap[V]) list() []signals.Receiver[V] {
	s.checkDeleted()
	return s.entries
}

func (s *orderedMap[V]) get(key string) (signals.Receiver[V], bool) {
	s.checkDeleted()

	idx, ok := s.index[key]
	if !ok {
		return nil, false
	}

	return s.entries[idx], true
}

func (s *orderedMap[V]) delete(key string) bool {
	idx, ok := s.index[key]
	if !ok {
		return false
	}

	delete(s.index, key)
	s.entries[idx] = nil
	s.deleted = append(s.deleted, idx)
	return true
}

func (s *orderedMap[V]) set(key string, value signals.Receiver[V]) bool {
	s.checkDeleted()

	if idx, ok := s.index[key]; ok {
		s.entries[idx] = value
		return true
	}

	s.index[key] = len(s.entries)
	s.entries = append(s.entries, value)
	s.cap = max(len(s.entries), s.cap)
	return true
}

func (s *orderedMap[V]) clear() {
	s.deleted = make([]int, s.cap)
	s.entries = make([]signals.Receiver[V], s.cap)
	clear(s.index)
}

func (s *orderedMap[V]) length() int {
	return len(s.index)
}
