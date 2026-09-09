package signals

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/Nigel2392/go-signals/internal/omap"
)

type gSignal[T any] signal[T]

func (s *gSignal[T]) Unwrap() Signal[T] {
	return (*signal[T])(s)
}
func (s *gSignal[T]) Name() string {
	return (*signal[T])(s).Name()
}
func (s *gSignal[T]) Send(ctx context.Context, value any) error {
	return (*signal[T])(s).Send(ctx, value.(T))
}
func (s *gSignal[T]) Connect(ctx context.Context, receivers ...Receiver[any]) error {
	panic("this should never be called")
}
func (s *gSignal[T]) Disconnect(ctx context.Context, other ...Receiver[any]) error {
	panic("this should never be called")
}
func (s *gSignal[T]) Clear(ctx context.Context) error {
	return (*signal[T])(s).Clear(ctx)
}
func (s *gSignal[T]) Listen(ctx context.Context, fn func(context.Context, Signal[any], any) error) (Receiver[any], error) {
	panic("this should never be called")
}

// Pool of signals.
//
// Can be used to store, retrieve and delete signals.
//
// Can also be used to send signals to receivers.
type GPool struct {
	mu sync.RWMutex
	m  *omap.OrderedMap[Signal[any]]
}

// Return a new pool of signals.
func NewGPool() *GPool {
	return &GPool{
		m: omap.NewOrderedMap(0, Signal[any].Name),
	}
}

// Load a signal from the pool.
// Use .Get() to fetch a signal from the pool.
// This will create one if it does not exist.
func (m *GPool) load[T any](signalName string) (value *signal[T], ok bool) {
	m.mu.RLock()
	v, ok := m.m.Get(signalName)
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}

	gs, ok := v.(*gSignal[T])
	if !ok {
		panic(fmt.Sprintf("signal %T is not of type %s", v, reflect.TypeFor[*gSignal[T]]()))
	}

	return (*signal[T])(gs), true
}

// Store a signal in the pool.
// Use .Get() to create a new signal if it does not exist.
func (m *GPool) store(signalName string, value Signal[any]) {
	m.mu.Lock()
	m.m.SetK(signalName, value)
	m.mu.Unlock()
}

// Delete a signal from the pool.
func (m *GPool) Delete(signalName string) {
	m.mu.Lock()
	m.m.Delete(signalName)
	m.mu.Unlock()
}

func (m *GPool) Size() int {
	return m.m.Length()
}

// Range over signals inside of the pool.
func (m *GPool) Range(f func(value Signal[any]) bool) {
	m.mu.RLock()
	sigs := slices.Clone(m.m.List())
	m.mu.RUnlock()

	for _, value := range sigs {
		if !f(value) {
			break
		}
	}
}

// Range over signals inside of the pool.
func (m *GPool) RangeT[T any](f func(value Signal[T]) (_continue bool)) {
	m.mu.RLock()
	sigs := slices.Clone(m.m.List())
	m.mu.RUnlock()

	//	if reflect.TypeFor[T]() == reflect.TypeFor[any]() {
	//		m.Range(*(*func(Signal[any]) bool)(unsafe.Pointer(&f)))
	//	}

	for _, value := range sigs {
		v, ok := value.(*gSignal[T])
		if !ok {
			continue
		}

		if !f((*signal[T])(v)) {
			break
		}
	}
}

// Send a signal inside of the signal pool, from the signal with the given name
// to all receivers that are connected to the signal.
func (m *GPool) Send[T any](ctx context.Context, name string, value T) error {
	var signal, ok = m.load[T](name)
	if !ok {
		return Err("signal not found")
	}
	return signal.Send(ctx, value)
}

// Send a signal globally, across all signals present in the pool that accept type T.
//
// This will send a signal to ALL receivers inside of this pool bound to signals of type T.
func (m *GPool) SendGlobal[T any](ctx context.Context, value T) error {
	var err error
	m.RangeT(func(sig Signal[T]) bool {
		err = sig.Send(ctx, value)
		return err == nil
	})
	return err
}

// Exists checks if a signal exists in the pool.
func (m *GPool) Exists(name string) bool {
	m.mu.RLock()
	ok := m.m.Has(name)
	m.mu.RUnlock()
	return ok
}

func (m *GPool) NewSignal[T any](ctx context.Context, name string) Signal[T] {
	s, ok := m.load[T](name)
	if ok {
		return s
	}

	s = &signal[T]{name: name, receivers: make([]Receiver[T], 0)}
	m.store(name, (*gSignal[T])(s))
	return s
}

// Register a receiver to a signal.
//
// This will register a receiver to a signal inside of the pool.
//
// If the signal does not exist, it will be created.
//
// This is a shorthand.
func (m *GPool) Listen[T any](ctx context.Context, name string, r func(context.Context, Signal[T], T) error) (Receiver[T], error) {
	return m.Get[T](name).Listen(ctx, r)
}

// Get a signal by name.
//
// ** Will initialize a new signal if none exists. **
func (m *GPool) Get[T any](name string) Signal[T] {
	return m.NewSignal[T](context.Background(), name)
}
