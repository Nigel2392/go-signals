package redis

import (
	"context"

	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/redis/go-redis/v9"
)

var _ pubsub.PubSub = (*redisPubSub)(nil)
var _ pubsub.Subscriber = (*redisSubscriber)(nil)

func PubSub(c redis.UniversalClient) pubsub.PubSub {
	return &redisPubSub{
		client: c,
	}
}

type redisPubSub struct {
	client      redis.UniversalClient
	channelOpts []redis.ChannelOption
}

func (s *redisPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	return s.client.Publish(ctx, topic, data).Err()
}

func (s *redisPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	ps := s.client.Subscribe(ctx, topic)
	sub := &redisSubscriber{
		pubsub: ps,
		ch:     ps.Channel(s.channelOpts...),
	}
	return sub, nil
}

type redisSubscriber struct {
	pubsub *redis.PubSub
	ch     <-chan *redis.Message
}

func (s *redisSubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-s.ch:
		if !ok || msg == nil {
			return nil, false
		}
		// Convert strings to []byte right as we pull it off the wire
		return []byte(msg.Payload), true
	default:
		// Queue is empty. Return instantly.
		return nil, false
	}
}

func (r *redisSubscriber) Close() error {
	return r.pubsub.Close()
}
