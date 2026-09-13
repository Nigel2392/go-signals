package pubsub2

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
)

func TestTPoolWaitLoop(t *testing.T) {
	pool := New(t.Context(), func() pubsub.PubSub {
		return NewMockPubSub(false)
	}).TPool[string]()

	sig := pool.NewSignal(context.Background(), "test_topic")

	receivedValue := make(chan string, 1)
	_, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		receivedValue <- val
		return nil
	})
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	if pool.Channel(t.Context()) != nil {
		t.Errorf("Expected channel to be nil until send")
	}

	// Trigger a send
	pool.Send(context.Background(), "test_topic", "loop message")

	if pool.Channel(t.Context()) == nil {
		t.Errorf("Channel not set correctly")
	}

	close(pool.Channel(t.Context())) // Close channel to exit the WaitLoop iter

	count := 0
	for handler, err := range pool.WaitLoop(context.Background()) {
		if err != nil {
			t.Errorf("WaitLoop error: %v", err)
		}

		if err := handler.Process(t.Context()); err != nil {
			t.Errorf("Process error: %v", err)
		}

		if handler.Value != "loop message" {
			t.Errorf("expected 'loop message', got '%v'", handler.Value)
		}
		count++
	}

	if count != 1 {
		t.Errorf("expected WaitLoop to yield 1 message, got %d", count)
	}

	// wait for goroutine in WaitLoop to finish
	select {
	case val := <-receivedValue:
		if val != "loop message" {
			t.Errorf("expected received value 'loop message', got '%s'", val)
		}
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for receiver")
	}
}

func BenchmarkSignalsTPool(b *testing.B) {

	pool := New(
		b.Context(),
		func() pubsub.PubSub {
			return NewMockPubSub(false)
		},
		pubsub.PoolClientInit(true),
		pubsub.PoolOnError(func(ctx context.Context, p *Pool, err error) {
			b.Log(string(debug.Stack()))
			b.Error(err)
		}),
	).TPool[string]()

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

	if int(incr.Load()) != (totalReceivers * b.N) {
		b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
	}

	pool.Close()
}
