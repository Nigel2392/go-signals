package pubsub

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals"
)

var totalReceivers = 32000

func connectSignal[T any](amount int, signal signals.Signal[T], receiverFunc func(ctx context.Context, signal signals.Signal[T], value T) error) {
	for i := 0; i < amount; i++ {
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(context.Background(), receiver)
	}
}

func BenchmarkSignals(b *testing.B) {

	pool := New[string](
		b.Context(),
		func() PubSub {
			return NewMockPubSub(false)
		},
		PoolOnError(func(ctx context.Context, p *Pool[string], err error) {
			b.Log(string(debug.Stack()))
			b.Error(err)
		}),
	)

	var incr = new(atomic.Int64)

	var signal = pool.NewSignal(b.Context(), uuid.New().String())
	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
		incr.Add(1)
		return nil
	})

	var wg sync.WaitGroup

	// benchmarks can only be done with WaitLoop!
	// this is the only way we can add waitgroups to ensure every task finished
	// at a possible (hidden) cost of benchmark performance.
	// hidden because we cannot consistently test [Pool.Loop] this way.
	go func() {
		for h, err := range pool.WaitLoop(b.Context()) {
			// b.Log(v, err)
			if err != nil {
				b.Error(err)
				return
			}
			h.Process(b.Context())
			wg.Done()
		}
	}()

	b.StartTimer()
	b.ResetTimer()

	for b.Loop() {
		b.StopTimer()
		wg.Add(1)
		b.StartTimer()

		err := signal.Send(b.Context(), "This is a signal message!")
		if err != nil {
			b.Error(err)
		}

		wg.Wait()
	}

	b.StopTimer()

	if int(incr.Load()) != (totalReceivers * b.N) {
		b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
	}

	pool.Close()
}

func BenchmarkSignalsParralel(b *testing.B) {

	pool := New[string](
		b.Context(),
		func() PubSub {
			return NewMockPubSub(false)
		},
		PoolOnError(func(ctx context.Context, p *Pool[string], err error) {
			b.Log(string(debug.Stack()))
			b.Error(err)
		}),
	)

	var incr = new(atomic.Int64)

	var signal = pool.NewSignal(b.Context(), uuid.New().String())
	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
		incr.Add(1)
		return nil
	})

	var wg sync.WaitGroup

	// benchmarks can only be done with WaitLoop!
	// this is the only way we can add waitgroups to ensure every task finished
	// at a possible (hidden) cost of benchmark performance.
	// hidden because we cannot consistently test [Pool.Loop] this way.
	go func() {
		for h, err := range pool.WaitLoop(b.Context()) {
			// b.Log(v, err)
			if err != nil {
				b.Error(err)
				return
			}
			h.Process(b.Context())
			wg.Done()
		}
	}()

	wg.Add(b.N)
	b.ResetTimer()

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			err := signal.Send(b.Context(), "This is a signal message!")
			if err != nil {
				b.Error(err)
			}
		}
	})

	wg.Wait()
	b.StopTimer()

	if int(incr.Load()) != (totalReceivers * b.N) {
		b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
	}

	pool.Close()
}

func TestSignalSend(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New[string](t.Context(), client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	if sig.Name() != "test_topic" {
		t.Errorf("expected topic test_topic, got %s", sig.Name())
	}

	err := sig.Send(context.Background(), "hello")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSignalSendAsync(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New[string](t.Context(), client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	errChan := signals.SendAsync(context.Background(), sig, "hello async")

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for async send")
	}
}

func TestSignalConnectListenDisconnect(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New[string](t.Context(), client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	receivedValue := make(chan string, 1)
	recv, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {

		msg := MessageFromContext(ctx)
		if msg.Channel != "test_topic" {
			t.Errorf("Message channel is not %q: %q", "test_topic", msg.Channel)
		}

		t.Logf("Value: %q", val)
		t.Logf("Message: %v", msg)

		if msg.Sender != PoolFromContext[Pool[string]](ctx).ID() {
			t.Errorf(
				"ID should match! %s != %s",
				msg.Sender,
				PoolFromContext[Pool[string]](ctx).ID(),
			)
		}

		receivedValue <- val
		return nil
	})

	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	err = sig.Send(context.Background(), "test message")
	if err != nil {
		t.Fatalf("Send error: %v", err)
	}

	// Trigger manual pull
	pool.doWork(context.Background())

	select {
	case val := <-receivedValue:
		if val != "test message" {
			t.Errorf("expected receivedValue to be 'test message', got '%s'", val)
		}
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for message processing")
	}

	err = sig.Disconnect(context.Background(), recv)
	if err != nil {
		t.Errorf("Disconnect error: %v", err)
	}
}

func TestSignalClear(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New[string](t.Context(), client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	_, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	err = sig.Clear(context.Background())
	if err != nil {
		t.Errorf("Clear error: %v", err)
	}

	pool.Mu.RLock()
	sub := pool.subscribers["test_topic"]
	pool.Mu.RUnlock()

	if sub != nil && sub.Receivers.Length() > 0 {
		t.Errorf("expected 0 receivers after clear, got %d", sub.Receivers.Length())
	}
}
