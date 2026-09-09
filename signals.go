package signals

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

var (
	_ Signal[int]      = (*signal[int])(nil)
	_ Transmitter[int] = (*signal[int])(nil)
)

// Underlying signal struct for the Signal interface.
//
// This will be used to send among receivers.
type signal[T any] struct {
	name      string        // Name of the signal.
	receivers []Receiver[T] // List of receivers.
	mu        sync.RWMutex  // Mutex for locking the signal.

	// caching to avoid locking mutexes during Send
	dirty  atomic.Bool
	cached []Receiver[T]
}

// Create a new signal.
func New[T any](name string) Signal[T] {
	return &signal[T]{
		name:      name,
		receivers: make([]Receiver[T], 0),
		mu:        sync.RWMutex{},
	}
}

func (s *signal[T]) getReceivers() []Receiver[T] {

	if s.dirty.Load() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cached = slices.Clone(s.receivers)
		s.dirty.Store(false)
		return s.cached
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
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
	for _, receiver := range receivers {
		err := receiver.Bind(ctx, s)
		if err != nil {
			return ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// append in one go instead of in the above loop
	s.receivers = append(s.receivers, receivers...)

	s.dirty.Store(
		s.dirty.Load() ||
			len(receivers) > 0,
	)

	return nil
}

// Disconnect a receiver from the signal.
func (s *signal[T]) Disconnect(ctx context.Context, other ...Receiver[T]) error {
	// Validate if any receivers have been connected.
	if len(other) == 0 {
		return ErrReceiver.WithCause(
			Err("did not provide any receivers to disconnect"),
		)
	}

	// manual lock management instead of defers
	// for higher perf and to prevent deadlocks (i.e. disconnect called within disconnect)
	s.mu.RLock()

	recvs := slices.Clone(s.receivers)
	idMap := make(map[string]struct{}, len(other))
	for _, r := range other {
		idMap[r.ID()] = struct{}{}
	}

	s.mu.RUnlock()

	// Disconnect the receivers.
	var newRecvs = make([]Receiver[T], 0, len(recvs))
	for _, recv := range recvs {
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

	// manual lock management instead of defers
	s.mu.Lock()
	s.receivers = newRecvs
	s.dirty.Store(true)
	s.mu.Unlock()
	return nil
}

// Clear the signal's receivers.
// This will disconnect all receivers from the signal.
func (s *signal[T]) Clear(ctx context.Context) error {
	// manual lock management instead of defers
	// for higher perf and to prevent deadlocks (i.e. Clear called within Clear)
	s.mu.RLock()
	recvs := slices.Clone(s.receivers)
	s.mu.RUnlock()

	for _, receiver := range recvs {
		err := receiver.Disconnect(ctx)
		if err != nil {
			return ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			)
		}
	}

	s.mu.Lock()
	s.receivers = make([]Receiver[T], 0)
	s.dirty.Store(true)
	s.mu.Unlock()
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

func (s *signal[T]) Receivers(ctx context.Context) []Receiver[T] {
	return s.getReceivers()
}
