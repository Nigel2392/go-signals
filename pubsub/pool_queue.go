package pubsub

import (
	"slices"
	"sync/atomic"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
)

type subscriber[T any] struct {
	pubsub    Subscriber
	receivers *omap.OrderedMap[T]

	_dirty  atomic.Bool
	_cached []signals.Receiver[T]
}

func (s *subscriber[T]) _undirtify() {
	s._cached = slices.Clone(s.receivers.List())
}

func (s *subscriber[T]) checkDirty() {
	if s._dirty.Load() {
		s._undirtify()
		s._dirty.Store(false)
	}
}

func (s *subscriber[T]) add(r signals.Receiver[T]) (isNew bool) {
	isNew = s.receivers.Set(r.ID(), r)
	if isNew {
		s._dirty.Store(true)
	}
	return isNew
}

func (s *subscriber[T]) del(r signals.Receiver[T]) (deleted bool) {
	deleted = s.receivers.Delete(r.ID())
	if deleted {
		s._dirty.Store(true)
	}
	return deleted
}

func (s *subscriber[T]) clear() {
	s._dirty.Store(s.receivers != nil && s.receivers.Length() > 0)
	s.receivers.Clear()
}

func (s *subscriber[T]) check(sigName string) error {
	if s.pubsub == nil {
		return nil
	}

	if s.receivers != nil && s.receivers.Length() > 0 {
		return nil
	}

	if err := s.pubsub.Close(); err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not close pubsub channel %q", sigName,
		)
	}

	s.pubsub = nil
	return nil
}
