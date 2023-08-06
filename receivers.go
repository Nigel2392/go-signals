package signals

import (
	"sync"
	"unsafe"
)

// Receiver interface
// This will be registered to any signals that it wants to receive.
// The receiver will be called when the signal is sent.
type Receiver[T any] interface {
	// Receives the signal and value from the signal.
	Receive(Signal[T], T) error

	// Disconnects the receiver from the signal.
	Disconnect() error

	// Sets the signal on the receiver instance for later use.
	Signal(...Signal[T]) Signal[T]

	// Return the unique ID of the receiver.
	ID() uint64
}

// Underlying receiver struct
type receiver[T any] struct {
	signal Signal[T]
	cb     func(Signal[T], T) error
	mu     sync.Mutex
}

// Initialize a new receiver
func NewRecv[T any](cb func(Signal[T], T) error) *receiver[T] {
	return &receiver[T]{cb: cb}
}

// Receives the signal and value from the signal.
func (r *receiver[T]) Receive(s Signal[T], value T) error {
	return r.cb(s, value)
}

// Disconnects the receiver from the signal.
func (r *receiver[T]) Disconnect() error {
	if r.signal == nil {
		return e("receiver is not connected to a signal")
	}
	r.signal.Disconnect(r)
	r.signal = nil
	return nil
}

// Sets the signal on the receiver instance for later use.
// Returns the signal if there is one.
// If the signal is already set, overwrite and return new value.
func (r *receiver[T]) Signal(signal ...Signal[T]) Signal[T] {
	if len(signal) > 0 {
		r.signal = signal[0]
	}
	return r.signal
}

// Return the unique ID of the receiver.
// This will be the memory address of the receiver.
func (r *receiver[T]) ID() uint64 {
	var addr = uintptr(unsafe.Pointer(r))
	return uint64(addr)
}
