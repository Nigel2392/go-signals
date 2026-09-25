package memory

import (
	"context"

	"github.com/Nigel2392/go-signals/internal/develop"
	"github.com/Nigel2392/go-signals/pubsub"
)

var (
	_ pubsub.PubSub       = (*memoryPubSub)(nil)
	_ pubsub.PubSubBinder = (*memoryPubSub)(nil)
	_ pubsub.Subscriber   = (*memorySubscriber)(nil)
)

func PubSub(async bool, channelSize ...int) pubsub.PubSub {
	async = develop.SyncOrAsyncBool(async)

	var chanSize = 128
	if len(channelSize) > 0 {
		chanSize = channelSize[0]
	}

	var ch chan pubsub.Message
	if !async {
		ch = make(chan pubsub.Message, chanSize)
	}

	return &memoryPubSub{
		publish:     ch,
		subscribers: make(map[string]memorySubscriber),
	}
}

type memoryPubSub struct {
	publish     chan pubsub.Message
	subscribers map[string]memorySubscriber
}

func (s *memoryPubSub) BindChannel(ctx context.Context, b pubsub.AbstractPool) {
	if s.publish != nil {
		b.SetChannel(ctx, s.publish)
	}
}

func (s *memoryPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	if s.publish != nil {
		s.publish <- pubsub.Message{
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

	sub <- pubsub.Message{
		Channel: topic,

		// data is an encoded pubsub.Message!!!
		Data: data,
	}

	return nil
}

func (s *memoryPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	sub, ok := s.subscribers[topic]
	if !ok {
		if s.publish == nil {
			sub = make(chan pubsub.Message, 100)
		} else {
			sub = s.publish
		}

		s.subscribers[topic] = sub
	}
	return sub, nil
}

type memorySubscriber chan pubsub.Message

func (s memorySubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-s:
		if !ok {
			return nil, false
		}
		return msg.Data, true
	default:
		return nil, false
	}
}

func (r memorySubscriber) Close() error {
	return nil
}
