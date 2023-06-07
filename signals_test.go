package signals_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
)

var pool = signals.NewPool[string]()

func TestSignals(t *testing.T) {
	var signalID = strconv.Itoa(int(time.Now().UnixNano()))
	var signal = pool.Get(signalID)

	var messages = make([]string, 0)

	var receiver = signals.NewRecv(func(signal signals.Signal[string], value ...string) error {
		t.Logf("Received %v from %s", value, signal.Name())
		messages = append(messages, value[0])
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

	newSignal := pool.Get(signalID)
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
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
	var messages = make([]string, 0)
	var receiver1 = signals.NewRecv(func(signal signals.Signal[string], value ...string) error {
		t.Log("Signal 1 fired.")
		messages = append(messages, value[0])
		return nil
	})
	var receiver2 = signals.NewRecv(func(signal signals.Signal[string], value ...string) error {
		t.Log("Signal 2 fired.")
		messages = append(messages, value[0])
		return nil
	})
	var receiver3 = signals.NewRecv(func(signal signals.Signal[string], value ...string) error {
		t.Log("Signal 3 fired.")
		messages = append(messages, value[0])
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

func connectSignal[T any](amount int, signal signals.Signal[T], receiverFunc func(signal signals.Signal[T], value ...T) error) {
	for i := 0; i < amount; i++ {
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(receiver)
	}
}

func BenchmarkSignals(b *testing.B) {
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))

	connectSignal(32000, signal, func(signal signals.Signal[string], value ...string) error { return nil })

	for i := 0; i < b.N; i++ {
		signal.Send("This is a signal message!")
	}
}

func TestMany(t *testing.T) {
	const amountCount = 32000

	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))

	connectSignal(amountCount, signal, func(signal signals.Signal[string], value ...string) error { return nil })

	for i := 0; i < amountCount; i++ {
		signal.Send("This is a signal message!")
	}
}

func TestSendAsync(t *testing.T) {
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
	var totalReceivers = 32000000

	connectSignal(totalReceivers, signal, func(signal signals.Signal[string], value ...string) error { return errors.New(value[0]) })

	var errChan chan error = signal.SendAsync("This is a signal message!")
	var errs []error = make([]error, 0)
	for err := range errChan {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != totalReceivers {
		t.Errorf("Expected %d errors, got %d", totalReceivers, len(errs))
	} else {
		t.Logf("Received %d errors", len(errs))
	}
}

func TestManyRecv(t *testing.T) {
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
	var totalReceivers = 32000000
	connectSignal(totalReceivers, signal, func(signal signals.Signal[string], value ...string) error { return errors.New(value[0]) })

	var err = signal.Send("This is a signal message!")

	if err != nil {
		if e, ok := signals.SignalError(err); ok {
			if e.Len() != totalReceivers {
				t.Errorf("Expected %d errors, got %d", totalReceivers, e.Len())
			} else {
				t.Logf("Received %d errors", e.Len())
			}
		} else {
			t.Errorf("Expected a signal error, got %s", e.Error())
		}
	} else {
		t.Errorf("Expected a signal error, got nil")
	}
}
