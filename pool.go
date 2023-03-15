package signals

import (
	"sync"
)

// Pool of signals.
//
// Can be used to store, retrieve and delete signals.
//
// Can also be used to send signals to receivers.
type Pool[T any] struct {
	mu sync.RWMutex
	m  map[string]Signal[T]
}

// Return a new pool of signals.
func NewPool[T any]() *Pool[T] {
	return &Pool[T]{
		m: make(map[string]Signal[T]),
	}
}

// Load a signal from the pool.
// Use .Get() to fetch a signal from the pool.
// This will create one if it does not exist.
func (m *Pool[T]) load(signalName string) (value Signal[T], ok bool) {
	m.mu.RLock()
	value, ok = m.m[signalName]
	m.mu.RUnlock()
	return
}

// Store a signal in the pool.
// Use .Get() to create a new signal if it does not exist.
func (m *Pool[T]) store(signalName string, value Signal[T]) {
	m.mu.Lock()
	m.m[signalName] = value
	m.mu.Unlock()
}

// Delete a signal from the pool.
func (m *Pool[T]) Delete(signalName string) {
	m.mu.Lock()
	delete(m.m, signalName)
	m.mu.Unlock()
}

// Range over signals inside of the pool.
func (m *Pool[T]) Range(f func(value Signal[T]) bool) {
	m.mu.RLock()
	for _, value := range m.m {
		if !f(value) {
			break
		}
	}
	m.mu.RUnlock()
}

// Send a signal inside of the signal pool, from the signal with the given name
// to all receivers that are connected to the signal.
func (m *Pool[T]) Send(name string, value ...T) error {
	var signal, ok = m.load(name)
	if !ok {
		return e("signal not found")
	}
	return signal.Send(value...)
}

// Send a signal globally, across all signals present in the pool.
//
// This will send a signal to ALL receivers inside of this pool.
func (m *Pool[T]) SendGlobal(value ...T) error {
	var err error
	m.Range(func(signal Signal[T]) bool {
		err = signal.Send(value...)
		return err == nil
	})
	return err
}

// Create or send a signal inside of the signal pool.
//
// This will send a signal to the receivers, if the signal already exists.
func (m *Pool[T]) CreateOrSend(name string, value ...T) error {
	var s, ok = m.load(name)
	if !ok {
		s = &signal[T]{name: name, receivers: make([]Receiver[T], 0), mu: &sync.Mutex{}}
		m.store(name, s)
	}
	return s.Send(value...)
}

// Register a receiver to a signal.
//
// This will register a receiver to a signal inside of the pool.
//
// If the signal does not exist, it will be created.
//
// This is a shorthand.
func (m *Pool[T]) Listen(name string, r func(Signal[T], ...T) error) (Receiver[T], error) {
	return m.Get(name).Listen(r)
}

// Get a signal by name.
//
// ** Will initialize a new signal if none exists. **
func (m *Pool[T]) Get(name string) Signal[T] {
	if signal, ok := m.load(name); ok {
		return signal
	}
	var s = &signal[T]{name: name, receivers: make([]Receiver[T], 0), mu: &sync.Mutex{}}
	m.store(name, s)
	return s
}
