package pubsub

import (
	"context"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type Encoder = encoder.Encoder

type PubSubPool[T any] interface {
	signals.SignalPool[T]
	Loop(ctx context.Context)
	Close()
}

type PubSub interface {
	Publish(ctx context.Context, topic string, data []byte) error
	Subscribe(ctx context.Context, topic string) (Subscriber, error)
}

type Subscriber interface {
	Close() error
	// TryReceive attempts a non-blocking read.
	// Returns (payload, true) if a message is immediately available.
	// Returns (nil, false) if the queue is empty.
	TryReceive() ([]byte, bool)
}

type Message struct {
	Channel string
	Data    []byte
}

type ChannelSubscriber interface {
	Subscriber
	Channel() <-chan *Message
}
