package pubsub

import (
	"context"
	"iter"

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

	// Loop is optimized to run in a separate goroutine, called by `go pool.Loop(ctx)`
	Loop(ctx context.Context)

	// WaitLoop is optimized to aggregate all central signals into the [PubSubPool]'s datachannel,
	// allowing for a lot more flexibility when it comes to testing and handling received data.
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

// Subscribers are returned by the [PubSub] interface, these subscribers are
// used to retrieve data to send to the receiver objects.
type Subscriber interface {
	Close() error
	// TryReceive attempts a non-blocking read.
	// Returns (payload, true) if a message is immediately available.
	// Returns (nil, false) if the queue is empty.
	TryReceive() ([]byte, bool)
}

// Messages transmitted internally across the [ChannelBinder]'s data channel.
//
// These can also be used by [PubSub] backends to allow for [PubSubPool.WaitLoop] functionality.
type Message struct {
	Channel string
	Data    []byte
}

// ChannelBinder is implemented by the [Pool] type to
// allow for the blocking WaitLoop function.
type ChannelBinder interface {
	Client() PubSub
	Channel() chan *Message
	SetChannel(ch chan *Message)
}
