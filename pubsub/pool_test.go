package pubsub

import (
	"context"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/elliotchance/orderedmap/v2"
)

func TestPoolInternalState(t *testing.T) {
	client := NewMockPubSub(true)

	customErrFn := func(p *Pool[string], err error) {
	}

	pool := New(client, PoolOnError(customErrFn))

	// Initial State Validation
	t.Run("InitialState", func(t *testing.T) {
		if pool.client != client {
			t.Errorf("expected client to be set")
		}
		if pool.signals == nil || len(pool.signals) != 0 {
			t.Errorf("expected empty signals map")
		}
		if pool.subscribers == nil || len(pool.subscribers) != 0 {
			t.Errorf("expected empty subscribers map")
		}
		if pool.encoder == nil {
			t.Errorf("expected encoder to be set")
		}
		if pool.closed.Load() {
			t.Errorf("expected closed to be false")
		}
	})

	// Signal Creation
	t.Run("NewSignal", func(t *testing.T) {
		sig := pool.NewSignal(context.Background(), "test_topic")
		if sig.Name() != "test_topic" {
			t.Errorf("expected signal name test_topic")
		}

		pool.mu.RLock()
		internalSig, ok := pool.signals["test_topic"]
		pool.mu.RUnlock()

		if !ok || internalSig != sig {
			t.Errorf("expected signal to be stored in pool signals map")
		}
	})

	// Receiver Connection
	t.Run("ConnectReceiver", func(t *testing.T) {
		sig := pool.NewSignal(context.Background(), "test_topic")
		recv, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
			return nil
		})

		if err != nil {
			t.Fatalf("Listen error: %v", err)
		}

		pool.mu.RLock()
		sub, ok := pool.subscribers["test_topic"]
		pool.mu.RUnlock()

		if !ok || sub == nil {
			t.Fatalf("expected subscriber to be created")
		}
		if sub.receivers.Len() != 1 {
			t.Errorf("expected 1 receiver in subscriber queue")
		}

		val, found := sub.receivers.Get(recv.ID())
		if !found || val != recv {
			t.Errorf("expected receiver to be in subscriber queue")
		}
	})

	// Test Pool Close
	t.Run("Close", func(t *testing.T) {
		pool.exit = make(chan struct{})
		pool.Close()

		if !pool.closed.Load() {
			t.Errorf("expected closed flag to be true")
		}

		if pool.exit != nil {
			t.Errorf("expected exit channel to be nil")
		}
	})
}

func TestPoolWaitLoop(t *testing.T) {
	client := NewMockPubSub(false)
	pool := New[string](client)

	if pool.Channel() == nil {
		t.Errorf("Channel not set correctly")
	}

	sig := pool.NewSignal(context.Background(), "test_topic")

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

	seq := pool.WaitLoop(context.Background(), true)

	count := 0
	for handler, err := range seq {
		if err != nil {
			t.Errorf("WaitLoop error: %v", err)
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
	pool := New(client, PoolTickTime[string](10*time.Millisecond))

	sig := pool.NewSignal(context.Background(), "test_topic")

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
	pool := New[string](client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	// Connect a receiver to create the subscriber
	recv, _ := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		return nil
	})

	pool.mu.RLock()
	sub := pool.subscribers["test_topic"]
	pool.mu.RUnlock()

	// Initial dirty flag should be true after add
	if !sub._dirty.Load() {
		t.Errorf("expected subscriber to be dirty after add")
	}

	// Call checkDirty to rebuild cache
	sub.checkDirty()

	if sub._dirty.Load() {
		t.Errorf("expected subscriber to not be dirty after checkDirty")
	}

	if len(sub._cached) != 1 || sub._cached[0] != recv {
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
	pool := New(client, PoolOnError(func(p *Pool[string], err error) {
		lastErr = err
	}))

	pool.NewSignal(context.Background(), "test_topic")

	// Manually construct a bad message in the mock subscriber's queue
	sub, _ := client.Subscribe(context.Background(), "test_topic")
	mockSub, ok := sub.(*MockSubscriber)
	if !ok {
		t.Fatalf("expected MockSubscriber")
	}

	// Push invalid JSON
	mockSub.push([]byte("{ invalid json }"), "test_topic")

	// Manually inject subscriber into pool
	pool.mu.Lock()
	q := orderedmap.NewOrderedMap[string, signals.Receiver[string]]()
	q.Set("dummy", &receiver[string]{id: "dummy"})

	pool.subscribers["test_topic"] = &subscriber[string]{
		pubsub:    mockSub,
		receivers: q,
	}
	pool.mu.Unlock()

	// doWork will pop from TryReceive, try to decode, and fail
	pool.doWork(context.Background())

	if lastErr == nil {
		t.Errorf("expected decoding error")
	}
}
