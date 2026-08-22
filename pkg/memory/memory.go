package memory

import (
	"context"

	"github.com/Nigel2392/go-signals/pubsub"
)

var _ pubsub.PubSub = (*memoryPubSub)(nil)
var _ pubsub.Subscriber = (*memorySubscriber)(nil)

func PubSub() pubsub.PubSub {
	return &memoryPubSub{
		subscribers: make(map[string]*memorySubscriber),
	}
}

type memoryPubSub struct {
	subscribers map[string]*memorySubscriber
}

func (s *memoryPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	if len(s.subscribers) == 0 {
		return nil
	}

	sub, ok := s.subscribers[topic]
	if !ok {
		return nil
	}

	sub.ch <- data
	return nil
}

func (s *memoryPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	sub, ok := s.subscribers[topic]
	if !ok {
		sub = &memorySubscriber{
			ch: make(chan []byte, 100),
		}
		s.subscribers[topic] = sub
	}
	return sub, nil
}

type memorySubscriber struct {
	ch chan []byte
}

func (s *memorySubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-s.ch:
		if !ok || msg == nil {
			return nil, false
		}
		return msg, true
	default:
		return nil, false
	}
}

func (s *memorySubscriber) Receive() <-chan []byte {
	return s.ch
}

func (r *memorySubscriber) Close() error {
	close(r.ch)
	return nil
}
