package signals

import (
	"context"
	"fmt"
	"unsafe"
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

// Underlying receiver struct
type receiver[T any] struct {
	signal Signal[T]
	cb     func(context.Context, Signal[T], T) error
}

// Initialize a new receiver
func NewRecv[T any](cb func(context.Context, Signal[T], T) error) *receiver[T] {
	return &receiver[T]{cb: cb}
}

// Receives the signal and value from the signal.
func (r *receiver[T]) Receive(ctx context.Context, s Signal[T], value T) error {
	return r.cb(ctx, s, value)
}

// Disconnects the receiver from the signal.
func (r *receiver[T]) Disconnect(ctx context.Context) error {
	if r.signal == nil {
		return Err("receiver is not connected to a signal")
	}
	r.signal = nil
	return nil
}

// Sets the signal on the receiver instance for later use.
func (r *receiver[T]) Bind(_ context.Context, signal Signal[T]) error {
	r.signal = signal
	return nil
}

// Returns the signal if there is one.
func (r *receiver[T]) Signal() Signal[T] {
	return r.signal
}

// Return the unique ID of the receiver.
// This will be the memory address of the receiver.
func (r *receiver[T]) ID() string {
	var addr = uintptr(unsafe.Pointer(r))
	return fmt.Sprint(uint64(addr))
}
