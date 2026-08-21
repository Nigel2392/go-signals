package redis

import (
	"github.com/Nigel2392/go-signals"
	"github.com/elliotchance/orderedmap/v2"
	"github.com/redis/go-redis/v9"
)

type subscriber[T any] struct {
	pubsub    *redis.PubSub
	receive   <-chan *redis.Message
	receivers *orderedmap.OrderedMap[string, signals.Receiver[T]]
}

func (s *subscriber[T]) add(r signals.Receiver[T]) (isNew bool) {
	return s.receivers.Set(r.ID(), r)
}

func (s *subscriber[T]) del(r signals.Receiver[T]) (deleted bool) {
	return s.receivers.Delete(r.ID())
}

func (s *subscriber[T]) check(sigName string) error {
	if s.pubsub == nil {
		return nil
	}

	if s.receivers != nil && s.receivers.Len() > 0 {
		return nil
	}

	if err := s.pubsub.Close(); err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not close pubsub channel %q", sigName,
		)
	}

	s.pubsub = nil
	s.receive = nil
	return nil
}
