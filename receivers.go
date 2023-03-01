package signals

import (
	"sync"
	"unsafe"
)

// Receiver interface
// This will be registered to any signals that it wants to receive.
// The receiver will be called when the signal is sent.
type Receiver interface {
	// Receives the signal and value from the signal.
	Receive(Signal, ...any) error

	// Disconnects the receiver from the signal.
	Disconnect() error

	// Sets the signal on the receiver instance for later use.
	Signal(...Signal) Signal

	// Return the unique ID of the receiver.
	ID() uint64
}

// Underlying receiver struct
type receiver struct {
	signal Signal
	cb     func(Signal, ...any) error
	mu     sync.Mutex
}

// Initialize a new receiver
func NewRecv(cb func(Signal, ...any) error) *receiver {
	return &receiver{cb: cb}
}

// Receives the signal and value from the signal.
func (r *receiver) Receive(s Signal, value ...any) error {
	return r.cb(s, value...)
}

// Disconnects the receiver from the signal.
func (r *receiver) Disconnect() error {
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
func (r *receiver) Signal(signal ...Signal) Signal {
	if len(signal) > 0 {
		r.signal = signal[0]
	}
	return r.signal
}

// Return the unique ID of the receiver.
// This will be the memory address of the receiver.
func (r *receiver) ID() uint64 {
	var addr = uintptr(unsafe.Pointer(r))
	return uint64(addr)
}
