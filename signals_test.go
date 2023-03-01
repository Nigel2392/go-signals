package signals_test

import (
	"errors"
	"testing"

	"github.com/Nigel2392/go-signals"
)

func TestSignals(t *testing.T) {
	var signal = signals.Get("mysignal")

	var messages = make([]string, 0)

	var receiver = signals.NewRecv(func(signal signals.Signal, value ...any) error {
		t.Logf("Received %v from %s", value, signal.Name())
		messages = append(messages, value[0].(string))
		return nil
	})

	signal.Connect(receiver)

	var err = signal.Send("This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}

	signal.Disconnect(receiver)

	err = signal.Send("This is a signal message!")
	if err == nil {
		t.Errorf("Expected error, got nil")
	}

	newSignal := signals.Get("mysignal")
	signal.Connect(receiver)
	err = newSignal.Send("This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestMultiple(t *testing.T) {
	var signal = signals.Get("mysignal")
	var messages = make([]string, 0)
	var receiver1 = signals.NewRecv(func(signal signals.Signal, value ...any) error {
		t.Log("Signal 1 fired.")
		messages = append(messages, value[0].(string))
		return nil
	})
	var receiver2 = signals.NewRecv(func(signal signals.Signal, value ...any) error {
		t.Log("Signal 2 fired.")
		messages = append(messages, value[0].(string))
		return nil
	})
	var receiver3 = signals.NewRecv(func(signal signals.Signal, value ...any) error {
		t.Log("Signal 3 fired.")
		messages = append(messages, value[0].(string))
		return nil
	})

	signal.Connect(receiver1, receiver2, receiver3)

	var err = signal.Send("This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	signal.Disconnect(receiver1, receiver3)

	err = signal.Send("This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages total, got %d", len(messages))
	}

}

func connectSignal(amount int, signal signals.Signal, receiverFunc func(signal signals.Signal, value ...any) error) {
	for i := 0; i < amount; i++ {
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(receiver)
	}
}

func BenchmarkSignals(b *testing.B) {
	var signal = signals.Get("mysignal")

	connectSignal(32000, signal, func(signal signals.Signal, value ...any) error { return nil })

	for i := 0; i < b.N; i++ {
		signal.Send("This is a signal message!")
	}
}

func TestMany(t *testing.T) {
	const amountCount = 32000

	var signal = signals.Get("mysignal")

	connectSignal(amountCount, signal, func(signal signals.Signal, value ...any) error { return nil })

	for i := 0; i < amountCount; i++ {
		signal.Send("This is a signal message!")
	}
}

func TestSendAsync(t *testing.T) {
	var signal = signals.Get("mysignal")
	var totalReceivers = 3200000

	connectSignal(totalReceivers, signal, func(signal signals.Signal, value ...any) error { return errors.New(value[0].(string)) })

	var errChan = signal.SendAsync("This is a signal message!")
	var errors int = 0
	for err := range errChan {
		if err != nil {
			// t.Logf("Received error: %s", err.Error())
			errors++
		}
	}
	if errors != totalReceivers {
		t.Errorf("Expected %d errors, got %d", totalReceivers, len(errChan))
	}
}
