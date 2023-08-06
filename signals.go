package signals

import (
	"fmt"
	"runtime"
	"sync"
)

// Signal interface.
//
// Used for sending messages to receivers.
type Signal[T any] interface {
	// Return the name of the signal.
	Name() string
	// Send a message across the signal's receivers.
	Send(T) error
	// Send a message across the signal's receivers asynchronously.
	SendAsync(T) chan error
	// Connect a list of receivers to the signal.
	Connect(...Receiver[T]) error
	// Disconnect a list of receivers from a signal.
	Disconnect(...Receiver[T])
	// Listen for a signal.
	Listen(func(Signal[T], T) error) (Receiver[T], error)
	// Clear all receivers for the signal.
	Clear()
}

// Underlying signal struct for the Signal interface.
//
// This will be used to send among receivers.
type signal[T any] struct {
	name      string        // Name of the signal.
	receivers []Receiver[T] // List of receivers.
	mu        *sync.Mutex   // Mutex for locking the signal.
}

// Create a new signal.
func New[T any](name string) Signal[T] {
	return &signal[T]{
		name:      name,
		receivers: make([]Receiver[T], 0),
		mu:        &sync.Mutex{},
	}
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
func (s *signal[T]) Send(value T) error {
	// Check if there are any receivers.
	if len(s.receivers) == 0 {
		return e("no receivers")
	}

	// Lock the signal so that we can't add
	// or remove receivers while we're sending.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Send the signal to each receiver.
	var err error
	var errs []error = make([]error, 0)
	for _, receiver := range s.receivers {
		err = receiver.Receive(s, value)
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Return an error if any of the receivers returned an error.
	if len(errs) > 0 {
		return e(fmt.Sprintf("error sending signal to %d receivers", len(errs)), errs...)
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
func (s *signal[T]) SendAsync(value T) chan error {
	// Lock the signal so that we can't add
	// or remove receivers while we're sending.

	// Send the signal to each receiver.
	var errChan chan error = make(chan error, len(s.receivers))
	go func() {
		var wg sync.WaitGroup
		defer wg.Wait()
		defer close(errChan)

		s.mu.Lock()
		defer s.mu.Unlock()

		wg.Add(len(s.receivers))
		for _, receiver := range s.receivers {
			// Create a new goroutine for each receiver.
			go func(receiver Receiver[T], wg *sync.WaitGroup) {
				defer wg.Done()
				errChan <- receiver.Receive(s, value)
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
func (s *signal[T]) Connect(receivers ...Receiver[T]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, receiver := range receivers {
		receiver.Signal(s)
		s.receivers = append(s.receivers, receiver)
	}
	return nil
}

// Disconnect a receiver from the signal.
func (s *signal[T]) Disconnect(other ...Receiver[T]) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate if any receivers have been connected.
	if len(other) == 0 {
		panic("did not provide any receivers to disconnect")
	}

	// Disconnect the receivers.
	var deleted int
	for i := range s.receivers {
		var index = i - deleted
		for _, o := range other {
			if s.receivers[index].ID() == o.ID() {
				o.Signal(nil)
				s.receivers = append(s.receivers[:index], s.receivers[index+1:]...)
				deleted++
			}
		}
	}
}

// Clear the signal's receivers.
// This will disconnect all receivers from the signal.
func (s *signal[T]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, receiver := range s.receivers {
		receiver.Disconnect()
	}

	s.receivers = make([]Receiver[T], 0)
}

// Listen for a signal.
//
// This will create a new receiver, and connect it to the signal.
func (s *signal[T]) Listen(fn func(Signal[T], T) error) (Receiver[T], error) {
	var receiver Receiver[T] = NewRecv(fn)
	var err = s.Connect(receiver)
	return receiver, err
}
