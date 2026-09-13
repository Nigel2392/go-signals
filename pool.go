package signals

import (
	"context"
	"sync"

	"github.com/Nigel2392/go-signals/internal/omap"
)

// Pool of signals.
//
// Can be used to store, retrieve and delete signals.
//
// Can also be used to send signals to receivers.
type Pool[T any] struct {
	mu sync.RWMutex
	m  *omap.OrderedMap[Signal[T]]
}

// Return a new pool of signals.
func NewPool[T any]() *Pool[T] {
	return &Pool[T]{
		m: omap.NewOrderedMap(0, Signal[T].Name),
	}
}

// Load a signal from the pool.
// Use .Get() to fetch a signal from the pool.
// This will create one if it does not exist.
func (m *Pool[T]) load(signalName string) (value Signal[T], ok bool) {
	m.mu.RLock()
	value, ok = m.m.Get(signalName)
	m.mu.RUnlock()
	return
}

// Store a signal in the pool.
// Use .Get() to create a new signal if it does not exist.
func (m *Pool[T]) store(signalName string, value Signal[T]) {
	m.mu.Lock()
	m.m.SetK(signalName, value)
	m.mu.Unlock()
}

// Delete a signal from the pool.
func (m *Pool[T]) Delete(signalName string) {
	m.mu.Lock()
	m.m.Delete(signalName)
	m.mu.Unlock()
}

func (m *Pool[T]) Size() int {
	return m.m.Length()
}

// Range over signals inside of the pool.
func (m *Pool[T]) Range(f func(value Signal[T]) bool) {
	m.mu.RLock()
	for _, value := range m.m.List() {
		if !f(value) {
			break
		}
	}
	m.mu.RUnlock()
}

// Send a signal inside of the signal pool, from the signal with the given name
// to all receivers that are connected to the signal.
func (m *Pool[T]) Send(ctx context.Context, name string, value T) error {
	var signal, ok = m.load(name)
	if !ok {
		return Err("signal not found")
	}
	return signal.Send(ctx, value)
}

// Send a signal globally, across all signals present in the pool.
//
// This will send a signal to ALL receivers inside of this pool.
func (m *Pool[T]) SendGlobal(ctx context.Context, value T) error {
	var err error
	m.Range(func(signal Signal[T]) bool {
		err = signal.Send(ctx, value)
		return err == nil
	})
	return err
}

// Exists checks if a signal exists in the pool.
func (m *Pool[T]) Exists(name string) bool {
	_, ok := m.load(name)
	return ok
}

func (m *Pool[T]) NewSignal(ctx context.Context, name string) Signal[T] {
	s, ok := m.load(name)
	if ok {
		return s
	}

	s = &signal[T]{name: name, receivers: make([]Receiver[T], 0)}
	m.store(name, s)
	return s
}

// Register a receiver to a signal.
//
// This will register a receiver to a signal inside of the pool.
//
// If the signal does not exist, it will be created.
//
// This is a shorthand.
func (m *Pool[T]) Listen(ctx context.Context, name string, r func(context.Context, Signal[T], T) error) (Receiver[T], error) {
	return m.Get(name).Listen(ctx, r)
}

// Get a signal by name.
//
// ** Will initialize a new signal if none exists. **
func (m *Pool[T]) Get(name string) Signal[T] {
	return m.NewSignal(context.Background(), name)
}
