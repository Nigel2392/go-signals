package pubsub

import (
	"context"
	"testing"

	"github.com/Nigel2392/go-signals"
)

func TestReceiver(t *testing.T) {
	client := NewMockPubSub(true)
	pool := New[string](client)

	sig := pool.NewSignal(context.Background(), "test_topic")

	called := false
	recv, err := sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], val string) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}

	if recv.ID() == "" {
		t.Errorf("expected receiver to have an ID")
	}

	if recv.Signal() != sig {
		t.Errorf("expected receiver to be bound to signal")
	}

	err = recv.Receive(context.Background(), sig, "hello")
	if err != nil {
		t.Errorf("Receive error: %v", err)
	}
	if !called {
		t.Errorf("expected receiver callback to be called")
	}

	err = recv.Disconnect(context.Background())
	if err != nil {
		t.Errorf("Disconnect error: %v", err)
	}

	if recv.Signal() != nil {
		t.Errorf("expected receiver to be unbound after disconnect")
	}
}
