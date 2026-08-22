package pubsub

import (
	"context"
	"iter"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type Encoder = encoder.Encoder

type PubSubPool[T any] interface {
	signals.SignalPool[T]
	Loop(ctx context.Context)
	Send(ctx context.Context, topic string, value T) error
	ReceiveData(ctx context.Context) iter.Seq2[T, error]
	Close()
}

type PubSub interface {
	Publish(ctx context.Context, topic string, data []byte) error
	Subscribe(ctx context.Context, topic string) (Subscriber, error)
}

type ChannelBinder interface {
	Client() PubSub
	Channel() chan *Message
	SetChannel(ch chan *Message)
}

type PubSubBinder interface {
	BindChannel(ChannelBinder)
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
