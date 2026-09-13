package subscriber

import (
	"slices"
	"sync/atomic"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
)

// this MUST mirror github.com/Nigel2392/go-signals/pubsub.Subscriber
type pubsubImpl interface {
	Close() error
	TryReceive() ([]byte, bool)
}

type Subscriber[T any] struct {
	Pubsub    pubsubImpl
	Receivers *omap.OrderedMap[signals.Receiver[T]]

	Dirty  atomic.Bool
	Cached []signals.Receiver[T]
}

func (s *Subscriber[T]) Undirtify() {
	s.Cached = slices.Clone(s.Receivers.List())
}

func (s *Subscriber[T]) CheckDirty() {
	if s.Dirty.Load() {
		s.Undirtify()
		s.Dirty.Store(false)
	}
}

func (s *Subscriber[T]) Add(r signals.Receiver[T]) (isNew bool) {
	isNew = s.Receivers.Set(r)
	if isNew {
		s.Dirty.Store(true)
	}
	return isNew
}

func (s *Subscriber[T]) Del(r signals.Receiver[T]) (deleted bool) {
	deleted = s.Receivers.Delete(r.ID())
	if deleted {
		s.Dirty.Store(true)
	}
	return deleted
}

func (s *Subscriber[T]) Clear() {
	s.Dirty.Store(s.Receivers != nil && s.Receivers.Length() > 0)
	s.Receivers.Clear()
}

func (s *Subscriber[T]) Check(sigName string) error {
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
