package signals

import (
	"context"
	"fmt"
	"iter"
	"runtime"
	"slices"
	"sync"
)

type Transmitter[T any] interface {
	Receivers(ctx context.Context) (_len int, _range iter.Seq[Receiver[T]])
	Transmit(ctx context.Context, value T, recv Receiver[T]) error
}

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

var (
	_ Signal[any]      = (*signal[any])(nil)
	_ Transmitter[any] = (*signal[any])(nil)
)

type cache[T any] struct {
	dirty bool
	data  []Receiver[T]
}

// Underlying signal struct for the Signal interface.
//
// This will be used to send among receivers.
type signal[T any] struct {
	name      string        // Name of the signal.
	receivers []Receiver[T] // List of receivers.
	mu        *sync.RWMutex // Mutex for locking the signal.

	cached cache[T]
}

// Create a new signal.
func New[T any](name string) Signal[T] {
	return &signal[T]{
		name:      name,
		receivers: make([]Receiver[T], 0),
		mu:        &sync.RWMutex{},
	}
}

func (s *signal[T]) getReceivers() []Receiver[T] {
	s.mu.RLock()
	isDirty := s.cached.dirty
	cached := s.cached.data
	s.mu.RUnlock()

	if !isDirty {
		return cached
	}

	s.mu.Lock()
	s.cached.data = slices.Clone(s.receivers)
	s.cached.dirty = false
	s.mu.Unlock()

	return s.cached.data
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

// Send a signal to all receivers asynchronously.
//
// Will error if there are no receivers.
//
// Returns an error, if any of the receivers return an error.
//
// This function is not fully tested, and might produce unexpected results.
//
// This function also will not check if there are any receivers.
//
// Returns a channel which will contain all errors from the receivers.
func (s *signal[T]) SendAsync(ctx context.Context, value T) chan error {
	recvs := s.getReceivers()

	// Check if there are any receivers.
	if len(recvs) == 0 {
		return nil
	}

	// Send the signal to each receiver.
	var errChan chan error = make(chan error, len(recvs))
	go func() {
		var wg sync.WaitGroup
		defer wg.Wait()
		defer close(errChan)

		wg.Add(len(recvs))
		for _, receiver := range recvs {
			// Create a new goroutine for each receiver.
			go func(receiver Receiver[T], wg *sync.WaitGroup) {
				defer wg.Done()
				errChan <- receiver.Receive(ctx, s, value)
			}(receiver, &wg)
			// Yield the goroutine.
			runtime.Gosched()
		}
		wg.Wait()
	}()

	return errChan
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

	s.cached.dirty = len(receivers) > 0

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
	s.cached.dirty = true

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
	s.cached.dirty = true
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

func SignalSend[T any](ctx context.Context, sig Signal[T], value T) error {
	t, ok := sig.(Transmitter[T])
	if !ok {
		return ErrUnsupported.Wrapf(
			"%T is not of type signals.Transmitter[T]", sig,
		)
	}

	var err error
	var errs []error = make([]error, 0)
	var _, _range = t.Receivers(ctx)
	for receiver := range _range {
		err = t.Transmit(ctx, value, receiver)
		if err != nil {
			errs = append(errs, err)
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

func SignalSendAsync[T any](ctx context.Context, sig Signal[T], value T) <-chan error {
	t, ok := sig.(Transmitter[T])
	if !ok {
		return sig.SendAsync(ctx, value)
	}

	var lenRecv, recvIter = t.Receivers(ctx)
	var errChan chan error = make(chan error, lenRecv)
	go func() {
		var wg sync.WaitGroup
		defer wg.Wait()
		defer close(errChan)

		wg.Add(lenRecv)

		for receiver := range recvIter {
			// Create a new goroutine for each receiver.
			go func(receiver Receiver[T], wg *sync.WaitGroup) {
				defer wg.Done()
				errChan <- t.Transmit(ctx, value, receiver)
			}(receiver, &wg)
			// Yield the goroutine.
			runtime.Gosched()
		}
		wg.Wait()
	}()

	return errChan
}
