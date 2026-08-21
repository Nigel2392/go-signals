package redis

import (
	"context"

	"github.com/Nigel2392/go-signals"
)

type receiver[T any] struct {
	id  string
	sig signals.Signal[T]
	cb  func(context.Context, signals.Signal[T], T) error
}

// Return the unique ID of the receiver.
func (r *receiver[T]) ID() string {
	return r.id
}

// Sets the signal on the receiver instance for later use.
func (r *receiver[T]) Bind(ctx context.Context, signal signals.Signal[T]) error {
	r.sig = signal
	return nil
}

// Returns the signal if there is one.
func (r *receiver[T]) Signal() signals.Signal[T] {
	return r.sig
}

// Receives the signal object and value
func (r *receiver[T]) Receive(ctx context.Context, s signals.Signal[T], val T) error {
	return r.cb(ctx, s, val)
}

// Disconnects the receiver from the signal.
func (r *receiver[T]) Disconnect(ctx context.Context) error {
	if r.sig == nil {
		return signals.Err("receiver is not connected to a signal")
	}
	r.sig.Disconnect(ctx, r)
	r.sig = nil
	return nil
}
