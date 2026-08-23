package pubsub

import (
	"context"
	"encoding/json"
	"iter"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

// Custom encoders can be provided to easily serialize and deserialize
// data across the pubsub signal pool's publishing lifecycle
type Encoder = encoder.Encoder

// The PubSubPool is the interface that [Pool] implements.
//
// It allows for easily implementing and using a subscribe- publish pattern.
//
// Current backends for this functionality are implemented in:
//
// * `github.com/Nigel2392/go-signals/pkg/memory`
// * `github.com/Nigel2392/go-signals/pkg/redis`
type PubSubPool[T any] interface {
	signals.SignalPool[T]
	ChannelBinder

	// The instance ID of the pool
	ID() uuid.UUID

	// Loop is optimized to run in a separate goroutine, called by `go pool.Loop(ctx)`
	Loop(ctx context.Context)

	// WaitLoop is optimized to aggregate all central signals into the [PubSubPool]'s datachannel,
	// allowing for a lot more flexibility when it comes to testing and handling received data.
	//
	// It does not rely on the ticker to retrieve values.
	//
	// This means that any values sent from a signal propagate as
	// quickly as possible, only being limited by the scheduler.
	WaitLoop(ctx context.Context) iter.Seq2[*Handler[T], error]

	// Send data across the pool for a topic to use.
	//
	// This method is also called by the [signal] type returned
	// from the pool's [PubSubPool.NewSignal] method.
	Send(ctx context.Context, topic string, value T) error

	// Stop all loops and close the pool down so no further processing can occur.
	Close()
}

// PubSub is the publisher backend used inside of the [PubSubPool]
//
// Implementations of PubSub are also allowed to implement [PubSubBinder],
// allowing direct access to the underlying data channel.
//
// Current uses of the data channel can be found in
// [memory_test.go/BenchmarkSignals] and [redis_test.go/BenchmarkSignals]
type PubSub interface {
	Publish(ctx context.Context, topic string, data []byte) error
	Subscribe(ctx context.Context, topic string) (Subscriber, error)
}

// Bind a [PubSub] to a [Pool] type.
type PubSubBinder interface {
	BindChannel(ChannelBinder)
}

// PubSubMsgMaker allows for [PubSub] objects
// to add metadata or perform other changes on messages
type PubSubMsgMaker interface {
	MakeMessage(ctx context.Context, topic string, msg *Message, sending bool) *Message
}

// Subscribers are returned by the [PubSub] interface, these subscribers are
// used to retrieve data to send to the receiver objects.
type Subscriber interface {
	Close() error

	// TryReceive attempts a non-blocking read.
	// Returns (payload, true) if a message is immediately available.
	// Returns (nil, false) if the queue is empty.
	//
	// The returned payload should always be generated
	// from serializing the Message.
	TryReceive() ([]byte, bool)
}

// Messages are the primary object transmitted throughout the application.
//
// These can also be used by [PubSub] backends to allow for [PubSubPool.WaitLoop] functionality.
type Message struct {
	// The UUID of the sender pool
	Sender uuid.UUID

	// The channel this message is for/from
	Channel string

	// just in case json is your preferred encoding.
	//
	// internal detail:
	// when using the [chan *Message] from the [ChannelBinder]
	// the data sent through [PubSub.Publish] is also a [Message]
	Data json.RawMessage

	// Metadata belonging to the message
	// Think of possible session information, etc.
	Meta map[string]any
}

// ChannelBinder is implemented by the [Pool] type to
// allow for the blocking WaitLoop function.
type ChannelBinder interface {
	Client() PubSub
	Channel() chan *Message
	SetChannel(ch chan *Message)
}
