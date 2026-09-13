package signals_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"github.com/Nigel2392/go-signals"
)

func TestGlobalGSignals(t *testing.T) {
	var signalID = uuid.New().String()
	var signal = signals.Get[string](signalID)

	var messages = make([]string, 0)

	var receiver = signals.NewRecv(func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Logf("Received %v from %s", value, signal.Name())
		messages = append(messages, value)
		return nil
	})

	signal.Connect(t.Context(), receiver)

	var err = signal.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}

	signal.Disconnect(t.Context(), receiver)

	err = signal.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}

	newSignal := signals.Get[string](signalID)
	signal.Connect(t.Context(), receiver)
	err = newSignal.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestGlobalGMultiple(t *testing.T) {
	var signalName = uuid.New().String()
	var messages = make([]string, 0)
	var receiver1, _ = signals.Listen(t.Context(), signalName, func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 1 fired.")
		messages = append(messages, value)
		return nil
	})
	signals.Listen(t.Context(), signalName, func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 2 fired.")
		messages = append(messages, value)
		return nil
	})
	var receiver3, _ = signals.Listen(t.Context(), signalName, func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 3 fired.")
		messages = append(messages, value)
		return nil
	})

	var err = signals.Send(t.Context(), signalName, "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	sig := signals.Get[string](signalName)
	sig.Disconnect(t.Context(), receiver1, receiver3)

	err = sig.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages total, got %d", len(messages))
	}
}

func BenchmarkGlobalGSignals(b *testing.B) {
	b.StopTimer()
	var signal = signals.Get[string](uuid.New().String())
	var incr int

	connectSignal(TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
		incr++
		return nil
	})

	b.StartTimer()
	b.ResetTimer()

	for b.Loop() {
		signal.Send(b.Context(), "This is a signal message!")
	}

	if incr != TOTAL_AMOUNT*b.N {
		b.Fatalf("incr should be %d, got %d", TOTAL_AMOUNT*b.N, incr)
	}
}

var receiverAmounts = []int{TOTAL_AMOUNT / 8, TOTAL_AMOUNT / 4, TOTAL_AMOUNT / 2, TOTAL_AMOUNT}

func BenchmarkGlobalGSignalsAsync(b *testing.B) {
	var batchSizes = []int{
		10, 50, 100, 250, 500, 1000,
	}

	if signals.DEFAULT_BATCH_SIZE == 0 {
		batchSizes = []int{0}
		receiverAmounts = []int{TOTAL_AMOUNT}
	}

	for _, amount := range receiverAmounts {
		for _, size := range batchSizes {
			b.Run(fmt.Sprintf("Batch%d/%d", size, amount), func(b *testing.B) {
				b.StopTimer()
				var signal = signals.Get[int64](uuid.New().String())
				// dont use atomic int, or check the value for correctness unless DEFAULT_BATCH_SIZE != 0 (i.e. build tag batches = false)
				var incr int64

				connectSignal(amount, signal, func(ctx context.Context, signal signals.Signal[int64], value int64) error {
					incr += value
					return nil
				})

				ctx := signals.ContextWithBatchSize(b.Context(), size)

				b.StartTimer()
				b.ResetTimer()

				for b.Loop() {

					errCh := signals.SendAsync(ctx, signal, 1)
					for range errCh { // ensures that we actually wait for all receivers to finish
					}

				}

				if testing.Verbose() {
					b.Log(incr)
				}

				if signals.DEFAULT_BATCH_SIZE == 0 && incr != int64(amount*b.N) {
					b.Fatalf("incr should be %d, got %d", amount*b.N, incr)
				}
			})
		}
	}
}

func TestGlobalGMany(t *testing.T) {
	amountCount := TOTAL_AMOUNT

	var signal = signals.Get[string](uuid.New().String())

	connectSignal(amountCount, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return nil })

	for i := 0; i < amountCount; i++ {
		signal.Send(t.Context(), "This is a signal message!")
	}
}

func TestGlobalGSendAsync(t *testing.T) {
	var signal = signals.Get[string](uuid.New().String())
	var totalReceivers = TOTAL_AMOUNT

	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return errors.New(value) })

	var errChan <-chan error = signals.SendAsync(t.Context(), signal, "This is a signal message!")
	var errs []error = make([]error, 0)
	for err := range errChan {
		if err != nil {
			errs = append(errs, err)
		}
	}

	expectedCount := 1
	expectedInner := totalReceivers
	if signals.DEFAULT_BATCH_SIZE > 0 {
		expectedCount = totalReceivers / signals.DEFAULT_BATCH_SIZE
		expectedInner = signals.DEFAULT_BATCH_SIZE
	}

	if len(errs) != expectedCount {
		t.Fatalf("Expected %d grouped error, got %d", expectedCount, len(errs))
	}

	err, ok := signals.SignalError(errs[0])
	if !ok {
		t.Fatalf("Expected to retrieve signals.Error, got %T", errs[0])
	}

	if len(err.Errors) != expectedInner {
		t.Fatalf("Expected %d errors, got %d", expectedInner, len(errs))
	}
}

func TestGlobalGManyRecv(t *testing.T) {
	var signal = signals.Get[string](uuid.New().String())
	var totalReceivers = TOTAL_AMOUNT
	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return errors.New(value) })

	var err = signal.Send(t.Context(), "This is a signal message!")

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

func TestGlobalGNestedSignals_SameSignal(t *testing.T) {
	var signalID = uuid.New().String()
	var signal = signals.Get[string](signalID)

	var callCount int
	var maxCalls = 5

	var receiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[string], value string) error {
		callCount++
		t.Logf("Call %d: received %s", callCount, value)

		// Break condition to prevent infinite recursion / stack overflow
		if callCount < maxCalls {
			// Fire the exact same signal while currently inside its listener
			return sig.Send(t.Context(), "nested call")
		}
		return nil
	})

	signal.Connect(t.Context(), receiver)

	// If the mutex is not released before iterating listeners, this will deadlock instantly.
	var err = signal.Send(t.Context(), "initial call")
	if err != nil {
		t.Errorf("Expected no errors during nested sends, got: %s", err.Error())
	}

	if callCount != maxCalls {
		t.Errorf("Expected %d calls, got %d", maxCalls, callCount)
	}
}

func TestGlobalGNestedSignals_CrossTrigger(t *testing.T) {
	// Simulating your exact ORM scenario
	var preCreate = signals.Get[int]("queries.model.pre_create_test")
	var postCreate = signals.Get[uint]("queries.model.post_create_test")

	var preCount, postCount int

	var preReceiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[int], value int) error {
		preCount++
		t.Log("Pre-Create fired. Emitting Post-Create...")

		// Emitting a different signal from within the listener
		return postCreate.Send(t.Context(), 1)
	})

	var postReceiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[uint], value uint) error {
		postCount++
		t.Log("Post-Create fired.")
		return nil
	})

	preCreate.Connect(t.Context(), preReceiver)
	postCreate.Connect(t.Context(), postReceiver)

	// Fire the first signal
	var err = preCreate.Send(t.Context(), 1)
	if err != nil {
		t.Fatalf("Failed to execute cross-trigger: %s", err.Error())
	}

	// Verify both fired exactly once
	if preCount != 1 {
		t.Errorf("Expected preCount to be 1, got %d", preCount)
	}
	if postCount != 1 {
		t.Errorf("Expected postCount to be 1, got %d", postCount)
	}
}
