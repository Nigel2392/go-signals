package omap

// OrderedMap is a lightweight ordered unique collection.
// It preserves insertion order via a slice and provides
// O(1) deduplication via a map from key to slice index.
type OrderedMap[V any] struct {
	Cap     int
	entries []V
	deleted []int
	index   map[string]int
	key     func(V) string
}

func NewOrderedMap[V any](Cap int, getKey func(V) string) *OrderedMap[V] {
	return &OrderedMap[V]{
		Cap:     Cap,
		entries: make([]V, 0, Cap),
		deleted: make([]int, 0),
		index:   make(map[string]int, Cap),
		key:     getKey,
	}
}

func (s *OrderedMap[V]) checkdeleted() {
	if len(s.deleted) == 0 {
		return
	}

	del := make(map[int]struct{}, len(s.deleted))
	for _, idx := range s.deleted {
		del[idx] = struct{}{}
	}

	var zero V
	var newIdx int
	var entries = make([]V, 0, len(s.entries)-len(s.deleted))
	var index = make(map[string]int, len(s.entries)-len(s.deleted))
	for idx, r := range s.entries {
		if any(r) == any(zero) {
			continue
		}

		if _, ok := del[idx]; ok {
			// shouldn't be hit because of the above check
			// values get set to their zero values in [OrderedMap.Delete]
			continue
		}

		entries = append(entries, r)
		index[s.key(r)] = newIdx
		newIdx++
	}

	s.Cap = max(len(entries), s.Cap)
	s.deleted = make([]int, 0)
	s.index = index
	s.entries = entries
}

func (s *OrderedMap[V]) List() []V {
	s.checkdeleted()
	return s.entries
}

func (s *OrderedMap[V]) Has(key string) bool {
	s.checkdeleted()
	_, ok := s.index[key]
	return ok
}

func (s *OrderedMap[V]) Get(key string) (v V, b bool) {
	s.checkdeleted()

	idx, ok := s.index[key]
	if !ok {
		return v, false
	}

	return s.entries[idx], true
}

func (s *OrderedMap[V]) Delete(key string) bool {
	idx, ok := s.index[key]
	if !ok {
		return false
	}

	var zero V
	delete(s.index, key)
	s.entries[idx] = zero
	s.deleted = append(s.deleted, idx)
	return true
}

func (s *OrderedMap[V]) SetK(key string, value V) bool {
	s.checkdeleted()

	if idx, ok := s.index[key]; ok {
		s.entries[idx] = value
		return true
	}

	s.index[key] = len(s.entries)
	s.entries = append(s.entries, value)
	s.Cap = max(len(s.entries), s.Cap)
	return true
}

func (s *OrderedMap[V]) Set(value V) bool {
	return s.SetK(s.key(value), value)
}

func (s *OrderedMap[V]) Clear() {
	s.deleted = make([]int, 0, s.Cap)
	s.entries = make([]V, 0, s.Cap)
	clear(s.index)
}

func (s *OrderedMap[V]) Length() int {
	return len(s.index)
}
