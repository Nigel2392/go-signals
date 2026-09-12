package signals

import (
	"context"
)

// Receiver interface
// This will be registered to any signals that it wants to receive.
// The receiver will be called when the signal is sent.
type Receiver[T any] interface {
	// Receives the signal and value from the signal.
	Receive(context.Context, Signal[T], T) error

	// Disconnects the receiver from the signal.
	Disconnect(context.Context) error

	// Sets the signal on the receiver instance for later use.
	Bind(context.Context, Signal[T]) error

	// Retrieves the signal from the receiver instance
	Signal() Signal[T]

	// Return the unique ID of the receiver.
	ID() string
}

// Signal interface.
//
// Used for sending messages to receivers.
type Signal[T any] interface {
	// Return the name of the signal.
	Name() string

	// Send a message across the signal's receivers.
	Send(context.Context, T) error

	// Connect a list of receivers to the signal.
	Connect(context.Context, ...Receiver[T]) error

	// Disconnect a list of receivers from a signal.
	Disconnect(context.Context, ...Receiver[T]) error

	// Listen for a signal.
	Listen(context.Context, func(context.Context, Signal[T], T) error) (Receiver[T], error)

	// Clear all receivers for the signal.
	Clear(context.Context) error
}

// Allows for [SendAsync] to properly and uniformly work across multiple signal packages.
type Transmitter[T any] interface {
	Signal[T]
	Transmit(ctx context.Context, value T, recv Receiver[T]) (err error)
	Receivers(ctx context.Context) []Receiver[T]
}

// Default signal pool
//
// This will be used to store, retrieve and delete signals.
//
// New signals will be created and added to the pool if they do not exist.
//
// If you need a separate pool of signals, use NewPool() to create a new one!
var defaultSignalPool = NewGPool()

// This will send the signal to all receivers that are connected to the signal.
//
// Returns an error, if any of the receivers return an error.
func Send[T any](ctx context.Context, name string, value T) error {
	return defaultSignalPool.Send(ctx, name, value)
}

// Get a signal by name.
//
// Create a new one if it does not exist.
func Get[T any](name string) Signal[T] {
	return defaultSignalPool.Get[T](name)
}

// Register a receiver to a signal.
//
// This will register a receiver to a signal inside of the pool.
//
// If the signal does not exist, it will be created.
//
// This is a shorthand.
func Listen[T any](ctx context.Context, name string, r func(context.Context, Signal[T], T) error) (Receiver[T], error) {
	return defaultSignalPool.Listen(ctx, name, r)
}

type batchSizeContextKey struct{}

func ContextWithBatchSize(ctx context.Context, size int) context.Context {
	return context.WithValue(ctx, batchSizeContextKey{}, size)
}

func BatchSize(ctx context.Context) int {
	if bs, ok := ctx.Value(batchSizeContextKey{}).(int); ok && bs > 0 {
		return bs
	}
	return DEFAULT_BATCH_SIZE
}

// Asynchronously send the value across all receivers.
//
// Returns a channel of error.
//
// The returned channel may be nil when there are no receivers returned by said signal.
//
// If the signal does not implement the [Transmitter] interface, we will fall back to
// creating a goroutine (closure) where [Signal.Send] is called and the error (if any)
// returned through the channel.
func SendAsync[T any](ctx context.Context, sig Signal[T], val T) <-chan error {
	t, ok := sig.(Transmitter[T])
	if !ok {
		var errChan chan error = make(chan error, 1)
		go func() {
			defer close(errChan)
			if err := sig.Send(ctx, val); err != nil {
				errChan <- err
			}
		}()
		return errChan
	}

	return asyncReceive(ctx, sig, t.Receivers(ctx), val)
}
