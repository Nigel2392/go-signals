package pubsub

import (
	"slices"
	"sync/atomic"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
)

type Sub[T any] struct {
	Pubsub    Subscriber
	Receivers *omap.OrderedMap[signals.Receiver[T]]

	dirty  atomic.Bool
	cached []signals.Receiver[T]
}

func (s *Sub[T]) undirtify() {
	s.cached = slices.Clone(s.Receivers.List())
}

func (s *Sub[T]) checkDirty() {
	if s.dirty.Load() {
		s.undirtify()
		s.dirty.Store(false)
	}
}

func (s *Sub[T]) Add(r signals.Receiver[T]) (isNew bool) {
	isNew = s.Receivers.Set(r)
	if isNew {
		s.dirty.Store(true)
	}
	return isNew
}

func (s *Sub[T]) Del(r signals.Receiver[T]) (deleted bool) {
	deleted = s.Receivers.Delete(r.ID())
	if deleted {
		s.dirty.Store(true)
	}
	return deleted
}

func (s *Sub[T]) Clear() {
	s.dirty.Store(s.Receivers != nil && s.Receivers.Length() > 0)
	s.Receivers.Clear()
}

func (s *Sub[T]) Check(sigName string) error {
	if s.Pubsub == nil {
		return nil
	}

	if s.Receivers != nil && s.Receivers.Length() > 0 {
		return nil
	}

	if err := s.Pubsub.Close(); err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not close pubsub channel %q", sigName,
		)
	}

	s.Pubsub = nil
	return nil
}
