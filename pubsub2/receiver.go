package pubsub2

import (
	"context"
	"unsafe"

	"github.com/Nigel2392/go-signals"
)

type ifaceReceiver[T any] struct {
	signals.Receiver[T]
}

func (s *ifaceReceiver[T]) Unwrap() signals.Receiver[T] {
	return s.Receiver
}

// Sets the signal on the receiver instance for later use.
func (r *ifaceReceiver[T]) Bind(ctx context.Context, sig signals.Signal[any]) error {
	return r.Receiver.Bind(ctx, TypedSignal[T](sig))
}

// Returns the signal if there is one.
func (r *ifaceReceiver[T]) Signal() signals.Signal[any] {
	return (*wrappedSignal[T])(r.Receiver.Signal().(*signal[T]))
}

// Receives the signal object and value
func (r *ifaceReceiver[T]) Receive(ctx context.Context, s signals.Signal[any], val any) error {
	var ifaceVal = (*iface)(unsafe.Pointer(&s))
	var sig = (*wrappedSignal[T])(ifaceVal.ptr)
	return r.Receiver.Receive(ctx, (*signal[T])(sig), val.(T))
}

type wrappedReceiver[T any] receiver[T]

// Return the unique ID of the receiver.
func (r *wrappedReceiver[T]) ID() string {
	return r.id
}

func (s *wrappedReceiver[T]) Unwrap() signals.Receiver[T] {
	return (*receiver[T])(s)
}

// Sets the signal on the receiver instance for later use.
func (r *wrappedReceiver[T]) Bind(ctx context.Context, sig signals.Signal[any]) error {
	return (*receiver[T])(r).Bind(ctx, sig.(unwrapper[*signal[T]]).Unwrap())
}

// Returns the signal if there is one.
func (r *wrappedReceiver[T]) Signal() signals.Signal[any] {
	if r.sig == nil {
		return nil
	}

	return (*wrappedSignal[T])(r.sig)
}

type iface struct {
	typ uintptr
	ptr unsafe.Pointer
}

// Receives the signal object and value
func (r *wrappedReceiver[T]) Receive(ctx context.Context, s signals.Signal[any], val any) error {
	var ifaceVal = (*iface)(unsafe.Pointer(&s))
	var sig = (*wrappedSignal[T])(ifaceVal.ptr)
	return (*receiver[T])(r).Receive(ctx, (*signal[T])(sig), val.(T))
}

// Disconnects the receiver from the signal.
func (r *wrappedReceiver[T]) Disconnect(ctx context.Context) error {
	return (*receiver[T])(r).Disconnect(ctx)
}

type receiver[T any] struct {
	id  string
	sig *signal[T]
	cb  func(context.Context, signals.Signal[T], T) error
}

// Return the unique ID of the receiver.
func (r *receiver[T]) ID() string {
	return r.id
}

// Sets the signal on the receiver instance for later use.
func (r *receiver[T]) Bind(ctx context.Context, sig signals.Signal[T]) error {
	r.sig = sig.(*signal[T])
	return nil
}

// Returns the signal if there is one.
func (r *receiver[T]) Signal() signals.Signal[T] {
	if r.sig == nil {
		// apparently a typed nil doesn't count as nil here...
		// signals.Signal[T]((*signal)(nil)) != nil
		return nil
	}
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
