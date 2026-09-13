package redis

import (
	"context"
	"fmt"

	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/redis/go-redis/v9"
)

var _ pubsub.PubSub = (*redisPubSub)(nil)
var _ pubsub.PubSubBinder = (*redisPubSub)(nil)
var _ pubsub.Subscriber = (*redisSubscriber)(nil)

type MinimalClient interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// PubSub creates a new Redis PubSub client.
// If async is false, it allocates a channel to bind to the Pool's WaitLoop.
func PubSub(async bool, c any) any {
	var ch chan pubsub.Message
	if !async {
		ch = make(chan pubsub.Message)
	}

	rps := &redisPubSub{
		publish: ch,
	}

	// ensure lazy init whenever we can
	switch v := c.(type) {
	case func() *redis.Client:
		rps._clientFn = func() MinimalClient { return v() }
		return func() pubsub.PubSub { return rps }
	case func() redis.UniversalClient:
		rps._clientFn = func() MinimalClient { return v() }
		return func() pubsub.PubSub { return rps }
	case func() MinimalClient:
		rps._clientFn = v
		return func() pubsub.PubSub { return rps }
	case MinimalClient:
		rps._client = v
	default:
		panic(fmt.Sprintf(
			"%T is not of type *redis.Client|redis.UniversalClient] or func() [*redis.Client|redis.UniversalClient]", c,
		))
	}

	return rps
}

type redisPubSub struct {
	_clientFn   func() MinimalClient
	_client     MinimalClient
	channelOpts []redis.ChannelOption
	publish     chan pubsub.Message
}

func (s *redisPubSub) client() MinimalClient {
	if s._client != nil {
		return s._client
	}

	s._client = s._clientFn()

	return s._client
}

func (s *redisPubSub) BindChannel(ctx context.Context, b pubsub.ChannelBinder) {
	if s.publish != nil {
		b.SetChannel(ctx, s.publish)
	}
}

func (s *redisPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	return s.client().Publish(ctx, topic, data).Err()
}

func (s *redisPubSub) MakeMessage(ctx context.Context, topic string, message *pubsub.Message, sending bool) *pubsub.Message {
	if !sending {
		return message
	}

	if c, ok := s.client().(interface{ Options() *redis.Options }); ok {
		opts := c.Options()
		message.Meta["sender"] = map[string]any{
			"client_name": opts.ClientName,
			"username":    opts.Username,
		}
	}

	return message
}

func (s *redisPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	ps := s.client().Subscribe(ctx, topic)
	sub := &redisSubscriber{
		topic:  topic,
		pubsub: ps,
		// ch:     ps.Channel(s.channelOpts...),
	}

	// If we are in synchronous mode, forward messages to the centralized channel.
	// synchronous means the pool loop is blocking, instead of in a goroutine.
	if s.publish != nil {
		go sub.forward(ctx, s.publish)
	} else {
		sub.ch = ps.Channel(s.channelOpts...)
	}

	return sub, nil
}

type redisSubscriber struct {
	topic  string
	pubsub *redis.PubSub
	ch     <-chan *redis.Message
}

func (s *redisSubscriber) receiveMessage(ctx context.Context) (*redis.Message, error) {
	for {
		msg, err := s.pubsub.Receive(ctx)
		if err != nil {
			return &redis.Message{Channel: s.topic}, err
		}

		switch msg := msg.(type) {
		case *redis.Subscription, *redis.Pong: // Ignore.
		case *redis.Message:
			return msg, err
		default:
			return nil, fmt.Errorf("redis: unknown message: %T", msg)
		}
	}
}

func (s *redisSubscriber) forward(ctx context.Context, out chan<- pubsub.Message) {
	// Blocks until a message arrives.
	// Automatically breaks and exits when r.pubsub.Close() is called.
	for {
		msg, err := s.receiveMessage(ctx)
		// for msg := range s.ch {
		out <- pubsub.Message{
			Channel: msg.Channel,

			// payload is an encoded pubsub.Message!!!
			Data:  []byte(msg.Payload),
			Error: err,
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
