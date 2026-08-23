package redis

import (
	"context"

	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/redis/go-redis/v9"
)

var _ pubsub.PubSub = (*redisPubSub)(nil)
var _ pubsub.PubSubBinder = (*redisPubSub)(nil)
var _ pubsub.Subscriber = (*redisSubscriber)(nil)

// PubSub creates a new Redis PubSub client.
// If async is false, it allocates a channel to bind to the Pool's WaitLoop.
func PubSub(async bool, c redis.UniversalClient) pubsub.PubSub {
	var ch chan *pubsub.Message
	if !async {
		ch = make(chan *pubsub.Message)
	}

	return &redisPubSub{
		client:  c,
		publish: ch,
	}
}

type redisPubSub struct {
	client      redis.UniversalClient
	channelOpts []redis.ChannelOption
	publish     chan *pubsub.Message
}

func (s *redisPubSub) BindChannel(b pubsub.ChannelBinder) {
	if s.publish != nil {
		b.SetChannel(s.publish)
	}
}

func (s *redisPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	return s.client.Publish(ctx, topic, data).Err()
}

func (s *redisPubSub) MakeMessage(ctx context.Context, topic string, message *pubsub.Message, sending bool) *pubsub.Message {
	if !sending {
		return message
	}

	if c, ok := s.client.(*redis.Client); ok {
		opts := c.Options()
		message.Meta["sender"] = map[string]any{
			"client_name": opts.ClientName,
			"username":    opts.Username,
		}
	}

	return message
}

func (s *redisPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	ps := s.client.Subscribe(ctx, topic)
	sub := &redisSubscriber{
		pubsub: ps,
		ch:     ps.Channel(s.channelOpts...),
	}

	// If we are in synchronous mode, forward messages to the centralized channel.
	// synchronous means the pool loop is blocking, instead of in a goroutine.
	if s.publish != nil {
		go sub.forward(s.publish)
	}

	return sub, nil
}

type redisSubscriber struct {
	pubsub *redis.PubSub
	ch     <-chan *redis.Message
}

func (s *redisSubscriber) forward(out chan<- *pubsub.Message) {
	// Blocks until a message arrives.
	// Automatically breaks and exits when r.pubsub.Close() is called.
	for msg := range s.ch {
		out <- &pubsub.Message{
			Channel: msg.Channel,

			// payload is an encoded pubsub.Message!!!
			Data: []byte(msg.Payload),
		}
	}
}

func (s *redisSubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-s.ch:
		if !ok || msg == nil {
			return nil, false
		}
		return []byte(msg.Payload), true
	default:
		return nil, false
	}
}

func (r *redisSubscriber) Close() error {
	return r.pubsub.Close()
}
