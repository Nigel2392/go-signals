package signals

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"sync"
	"sync/atomic"
)

// Signal interface.
//
// Used for sending messages to receivers.
type Signal[T any] interface {
	// Return the name of the signal.
	Name() string

	// Send a message across the signal's receivers.
	Send(context.Context, T) error

	// Send a message across the signal's receivers asynchronously.
	SendAsync(context.Context, T) chan error

	// Connect a list of receivers to the signal.
	Connect(context.Context, ...Receiver[T]) error

	// Disconnect a list of receivers from a signal.
	Disconnect(context.Context, ...Receiver[T]) error

	// Listen for a signal.
	Listen(context.Context, func(context.Context, Signal[T], T) error) (Receiver[T], error)

	// Clear all receivers for the signal.
	Clear(context.Context) error
}

var _ Signal[any] = (*signal[any])(nil)

// Underlying signal struct for the Signal interface.
//
// This will be used to send among receivers.
type signal[T any] struct {
	name      string        // Name of the signal.
	receivers []Receiver[T] // List of receivers.
	mu        sync.Mutex    // Mutex for locking the signal.

	// caching to avoid locking mutexes during Send
	dirty  atomic.Bool
	cached []Receiver[T]
}

// Create a new signal.
func New[T any](name string) Signal[T] {
	return &signal[T]{
		name:      name,
		receivers: make([]Receiver[T], 0),
		mu:        sync.Mutex{},
	}
}

func (s *signal[T]) getReceivers() []Receiver[T] {

	if s.dirty.Load() {
		s.mu.Lock()
		s.cached = slices.Clone(s.receivers)
		s.dirty.Store(false)
		s.mu.Unlock()
	}

	return s.cached
}

// Return the name of the signal.
func (s *signal[T]) Name() string {
	return s.name
}

// Send a signal to all receivers.
//
// Will error if there are no receivers.
//
// Returns an error, if any of the receivers return an error.
func (s *signal[T]) Send(ctx context.Context, value T) error {
	recvs := s.getReceivers()

	// Check if there are any receivers.
	if len(recvs) == 0 {
		return nil
	}

	// Send the signal to each receiver.
	var err error
	var errs []error
	for _, receiver := range recvs {
		err = receiver.Receive(ctx, s, value)
		if err != nil {
			errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
		}
	}

	// Return an error if any of the receivers returned an error.
	if len(errs) > 0 {
		return ErrSignal.WithCause(Err(fmt.Sprintf(
			"error sending signal to %d receivers",
			len(errs)), errs...,
		))
	}

	return nil
}

// Connect a receiver to the signal.
// This will call the receiver's Signal, setting the receiver's signal to this signal.
func (s *signal[T]) Connect(ctx context.Context, receivers ...Receiver[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, receiver := range receivers {
		err := receiver.Bind(ctx, s)
		if err != nil {
			return ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			)
		}

		s.receivers = append(s.receivers, receiver)
	}

	s.dirty.Store(
		s.dirty.Load() ||
			len(receivers) > 0,
	)

	return nil
}

// Disconnect a receiver from the signal.
func (s *signal[T]) Disconnect(ctx context.Context, other ...Receiver[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate if any receivers have been connected.
	if len(other) == 0 {
		return ErrReceiver.WithCause(
			Err("did not provide any receivers to disconnect"),
		)
	}

	var idMap = make(map[string]struct{}, len(other))
	for _, r := range other {
		idMap[r.ID()] = struct{}{}
	}

	// Disconnect the receivers.
	var newRecvs = make([]Receiver[T], 0, len(s.receivers))
	for _, recv := range s.receivers {
		_, ok := idMap[recv.ID()]
		if !ok {
			newRecvs = append(newRecvs, recv)
			continue
		}

		if err := recv.Disconnect(ctx); err != nil {
			return ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", recv.ID(),
			)
		}
	}

	s.receivers = newRecvs
	s.dirty.Store(true)

	return nil
}

// Clear the signal's receivers.
// This will disconnect all receivers from the signal.
func (s *signal[T]) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, receiver := range s.receivers {
		err := receiver.Disconnect(ctx)
		if err != nil {
			return ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			)
		}
	}

	s.receivers = make([]Receiver[T], 0)
	s.dirty.Store(true)
	return nil
}

// Listen for a signal.
//
// This will create a new receiver, and connect it to the signal.
func (s *signal[T]) Listen(ctx context.Context, fn func(context.Context, Signal[T], T) error) (Receiver[T], error) {
	var receiver Receiver[T] = NewRecv(fn)
	var err = s.Connect(ctx, receiver)
	return receiver, err
}

func (s *signal[T]) Transmit(ctx context.Context, value T, recv Receiver[T]) (err error) {
	err = recv.Receive(ctx, s, value)
	if err != nil {
		err = ErrReceiver.WithCause(err).Wrapf(
			"receiver %q:", recv.ID(),
		)
	}
	return err
}

func (s *signal[T]) Receivers(ctx context.Context) (int, iter.Seq[Receiver[T]]) {
	recvs := s.getReceivers()

	// Check if there are any receivers.
	if len(recvs) == 0 {
		return 0, nil
	}

	return len(recvs), func(yield func(Receiver[T]) bool) {
		for _, v := range recvs {
			if !yield(v) {
				return
			}
		}
	}
}
