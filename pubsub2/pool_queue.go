package pubsub2

import (
	"slices"
	"sync/atomic"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/pubsub"
)

/*

	this file must stay mostly similar to pubsub/pool_queue.go

*/

type subscriber struct {
	pubsub    pubsub.Subscriber
	receivers *omap.OrderedMap[signals.Receiver[any]]

	_dirty  atomic.Bool
	_cached []signals.Receiver[any]
}

func (s *subscriber) _undirtify() {
	s._cached = slices.Clone(s.receivers.List())
}

func (s *subscriber) checkDirty() {
	if s._dirty.Load() {
		s._undirtify()
		s._dirty.Store(false)
	}
}

func (s *subscriber) add(r signals.Receiver[any]) (isNew bool) {
	isNew = s.receivers.Set(r)
	if isNew {
		s._dirty.Store(true)
	}
	return isNew
}

func (s *subscriber) del(r interface{ ID() string }) (deleted bool) {
	deleted = s.receivers.Delete(r.ID())
	if deleted {
		s._dirty.Store(true)
	}
	return deleted
}

func (s *subscriber) clear() {
	s._dirty.Store(s.receivers != nil && s.receivers.Length() > 0)
	s.receivers.Clear()
}

func (s *subscriber) check(sigName string) error {
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
