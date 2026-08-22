package memory

import (
	"context"

	"github.com/Nigel2392/go-signals/pubsub"
)

var _ pubsub.PubSub = (*memoryPubSub)(nil)
var _ pubsub.PubSubBinder = (*memoryPubSub)(nil)
var _ pubsub.Subscriber = (*memorySubscriber)(nil)

func PubSub(async bool) pubsub.PubSub {
	var ch chan *pubsub.Message
	if !async {
		ch = make(chan *pubsub.Message)
	}

	return &memoryPubSub{
		publish:     ch,
		subscribers: make(map[string]*memorySubscriber),
	}
}

type memoryPubSub struct {
	publish     chan *pubsub.Message
	subscribers map[string]*memorySubscriber
}

func (s *memoryPubSub) BindChannel(b pubsub.ChannelBinder) {
	if s.publish != nil {
		b.SetChannel(s.publish)
	}
}

func (s *memoryPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	if s.publish != nil {
		s.publish <- &pubsub.Message{
			Channel: topic,
			Data:    data,
		}
		return nil
	}

	if len(s.subscribers) == 0 {
		return nil
	}

	sub, ok := s.subscribers[topic]
	if !ok {
		return nil
	}

	sub.ch <- &pubsub.Message{
		Channel: topic,
		Data:    data,
	}
	return nil
}

func (s *memoryPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	sub, ok := s.subscribers[topic]
	if !ok {
		ch := s.publish
		if ch == nil {
			ch = make(chan *pubsub.Message, 100)
		}

		sub = &memorySubscriber{
			ch: ch,
		}

		s.subscribers[topic] = sub
	}
	return sub, nil
}

type memorySubscriber struct {
	ch chan *pubsub.Message
}

func (s *memorySubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-s.ch:
		if !ok || msg == nil {
			return nil, false
		}
		return msg.Data, true
	default:
		return nil, false
	}
}

func (r *memorySubscriber) Close() error {
	close(r.ch)
	return nil
}
