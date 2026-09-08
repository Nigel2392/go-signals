package pubsub2

import (
	"context"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/pubsub"
)

func TestPoolInternalState(t *testing.T) {
	client := NewMockPubSub(true)

	customErrFn := func(p *Pool, err error) {
	}

	pool := New(client, pubsub.PoolOnError(customErrFn))

	// Initial State Validation
	t.Run("InitialState", func(t *testing.T) {
		if pool.Client() != client {
			t.Errorf("expected client to be set")
		}
		if pool.signals == nil || len(pool.signals) != 0 {
			t.Errorf("expected empty signals map")
		}
		if pool.subscribers == nil || len(pool.subscribers) != 0 {
			t.Errorf("expected empty subscribers map")
		}
		if pool.Encoder == nil {
			t.Errorf("expected encoder to be set")
		}
		if pool.Closed.Load() {
			t.Errorf("expected closed to be false")
		}
	})

	// Signal Creation
	t.Run("NewSignal", func(t *testing.T) {
		sig := pool.NewSignal[string](context.Background(), "test_topic")
		if sig.Name() != "test_topic" {
			t.Errorf("expected signal name test_topic")
		}

		pool.Mu.RLock()
		internalSig, ok := pool.signals["test_topic"]
		pool.Mu.RUnlock()

		if !ok || TypedSignal[string](internalSig) != sig {
			t.Errorf("expected signal to be stored in pool signals map")
		}
	})

	// Receiver Connection
	t.Run("ConnectReceiver", func(t *testing.T) {
		sig := pool.NewSignal[string](context.Background(), "test_topic")
		recv, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
			return nil
		})

		if err != nil {
			t.Fatalf("Listen error: %v", err)
		}

		pool.Mu.RLock()
		sub, ok := pool.subscribers["test_topic"]
		pool.Mu.RUnlock()

		if !ok || sub == nil {
			t.Fatalf("expected subscriber to be created")
		}
		if sub.receivers.Length() != 1 {
			t.Errorf("expected 1 receiver in subscriber queue")
		}

		val, found := sub.receivers.Get(recv.ID())
		if !found || TypedReceiver[string](val) != recv {
			t.Errorf("expected receiver to be in subscriber queue")
		}
	})

	// Test Pool Close
	t.Run("Close", func(t *testing.T) {
		pool.Exit = make(chan struct{})
		pool.Close()

		if !pool.Closed.Load() {
			t.Errorf("expected closed flag to be true")
		}

		if pool.Exit != nil {
			t.Errorf("expected exit channel to be nil")
		}
	})
}

func TestPoolWaitLoop(t *testing.T) {
	client := NewMockPubSub(false)
	pool := New(client)

	if pool.Channel() == nil {
		t.Errorf("Channel not set correctly")
	}

	sig := pool.NewSignal[string](context.Background(), "test_topic")

	receivedValue := make(chan string, 1)
	_, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		receivedValue <- val
		return nil
	})
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	// Trigger a send
	pool.Send(context.Background(), "test_topic", "loop message")

	close(pool.Channel()) // Close channel to exit the WaitLoop iter

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

func TestPoolLoop(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New(client, pubsub.PoolTickTime(10*time.Millisecond))

	sig := pool.NewSignal[string](context.Background(), "test_topic")

	receivedValue := make(chan string, 1)
	_, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		receivedValue <- val
		return nil
	})
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Loop(ctx)

	err = sig.Send(context.Background(), "test message")
	if err != nil {
		t.Errorf("Send error: %v", err)
	}

	select {
	case val := <-receivedValue:
		if val != "test message" {
			t.Errorf("expected 'test message', got '%s'", val)
		}
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for message")
	}

	cancel()                          // This should stop the loop
	time.Sleep(20 * time.Millisecond) // Give loop time to exit

	pool.Close() // Should be safe to call again or if cancel didn't clean up
}

func TestPool_SubscriberCache(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New(client)

	sig := pool.NewSignal[string](context.Background(), "test_topic")

	// Connect a receiver to create the subscriber
	recv, _ := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		return nil
	})

	pool.Mu.RLock()
	sub := pool.subscribers["test_topic"]
	pool.Mu.RUnlock()

	// Initial dirty flag should be true after add
	if !sub._dirty.Load() {
		t.Errorf("expected subscriber to be dirty after add")
	}

	// Call checkDirty to rebuild cache
	sub.checkDirty()

	if sub._dirty.Load() {
		t.Errorf("expected subscriber to not be dirty after checkDirty")
	}

	if len(sub._cached) != 1 || TypedReceiver[string](sub._cached[0]) != recv {
		t.Errorf("expected cached slice to contain the receiver")
	}

	// Removing the receiver should set dirty again
	sig.Disconnect(context.Background(), recv)

	if !sub._dirty.Load() {
		t.Errorf("expected subscriber to be dirty after delete")
	}
}

func TestPool_DecodeErrorHandling(t *testing.T) {
	client := NewMockPubSub(true)

	var lastErr error
	pool := New(client, pubsub.PoolOnError(func(p *Pool, err error) {
		lastErr = err
	}))

	pool.NewSignal[string](context.Background(), "test_topic")

	// Manually construct a bad message in the mock subscriber's queue
	sub, _ := client.Subscribe(context.Background(), "test_topic")
	mockSub, ok := sub.(*MockSubscriber)
	if !ok {
		t.Fatalf("expected MockSubscriber")
	}

	// Push invalid JSON
	mockSub.push([]byte("{ invalid json }"), "test_topic")

	// Manually inject subscriber into pool
	pool.Mu.Lock()
	q := omap.NewOrderedMap[any](0)
	q.Set("dummy", &wrappedReceiver[any]{id: "dummy"})

	pool.subscribers["test_topic"] = &subscriber{
		pubsub:    mockSub,
		receivers: q,
	}
	pool.Mu.Unlock()

	// doWork will pop from TryReceive, try to decode, and fail
	pool.doWork(context.Background())

	if lastErr == nil {
		t.Errorf("expected decoding error")
	}
}
