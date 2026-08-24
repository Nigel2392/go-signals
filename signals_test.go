package signals_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
)

var TOTAL_AMOUNT = 32000
var pool = signals.NewPool[string]()

func TestSignals(t *testing.T) {
	var signalID = strconv.Itoa(int(time.Now().UnixNano()))
	var signal = pool.Get(signalID)

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

	newSignal := pool.Get(signalID)
	signal.Connect(t.Context(), receiver)
	err = newSignal.Send(t.Context(), "This is a signal message!")
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
	var receiver1 = signals.NewRecv(func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 1 fired.")
		messages = append(messages, value)
		return nil
	})
	var receiver2 = signals.NewRecv(func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 2 fired.")
		messages = append(messages, value)
		return nil
	})
	var receiver3 = signals.NewRecv(func(ctx context.Context, signal signals.Signal[string], value string) error {
		t.Log("Signal 3 fired.")
		messages = append(messages, value)
		return nil
	})

	signal.Connect(t.Context(), receiver1, receiver2, receiver3)

	var err = signal.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(messages))
	}

	signal.Disconnect(t.Context(), receiver1, receiver3)

	err = signal.Send(t.Context(), "This is a signal message!")
	if err != nil {
		t.Errorf("Expected no errors, got %s", err.Error())
	}
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages total, got %d", len(messages))
	}

}

func connectSignal[T any](amount int, signal signals.Signal[T], receiverFunc func(ctx context.Context, signal signals.Signal[T], value T) error) {
	for i := 0; i < amount; i++ {
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(context.Background(), receiver)
	}
}

func BenchmarkSignals(b *testing.B) {
	b.StopTimer()
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
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

func BenchmarkSignalsAsync(b *testing.B) {
	var batchSizes = []int{
		10, 50, 100, 250, 500, 1000,
	}

	if signals.DEFAULT_BATCH_SIZE == 0 {
		batchSizes = []int{0}
	}

	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("BenchmarkSignalsAsyncBatch%d", size), func(b *testing.B) {
			b.StopTimer()
			var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
			// dont use atomic int, or check the value for correctness unless DEFAULT_BATCH_SIZE != 0 (i.e. build tag batches = false)
			var incr int64

			connectSignal(TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
				incr++
				return nil
			})

			ctx := signals.ContextWithBatchSize(b.Context(), size)

			b.StartTimer()
			b.ResetTimer()

			for b.Loop() {

				errCh := signal.SendAsync(ctx, "This is a signal message!")
				for range errCh { // ensures that we actually wait for all receivers to finish
				}

			}

			b.Log(incr)

			if signals.DEFAULT_BATCH_SIZE == 0 && incr != int64(TOTAL_AMOUNT*b.N) {
				b.Fatalf("incr should be %d, got %d", TOTAL_AMOUNT*b.N, incr)
			}
		})
	}
}

func TestMany(t *testing.T) {
	amountCount := TOTAL_AMOUNT

	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))

	connectSignal(amountCount, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return nil })

	for i := 0; i < amountCount; i++ {
		signal.Send(t.Context(), "This is a signal message!")
	}
}

func TestSendAsync(t *testing.T) {
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
	var totalReceivers = TOTAL_AMOUNT

	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return errors.New(value) })

	var errChan chan error = signal.SendAsync(t.Context(), "This is a signal message!")
	var errs []error = make([]error, 0)
	for err := range errChan {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 1 {
		t.Fatalf("Expected %d grouped error, got %d", 1, len(errs))
	}

	err, ok := signals.SignalError(errs[0])
	if !ok {
		t.Fatalf("Expected to retrieve signals.Error, got %T", errs[0])
	}

	if len(err.Errors) != totalReceivers {
		t.Fatalf("Expected %d errors, got %d", totalReceivers, len(errs))
	}
}

func TestManyRecv(t *testing.T) {
	var signal = pool.Get(strconv.Itoa(int(time.Now().UnixNano())))
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

func TestNestedSignals_SameSignal(t *testing.T) {
	var signalID = strconv.Itoa(int(time.Now().UnixNano()))
	var signal = pool.Get(signalID)

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

func TestNestedSignals_CrossTrigger(t *testing.T) {
	// Simulating your exact ORM scenario
	var preCreate = pool.Get("queries.model.pre_create_test")
	var postCreate = pool.Get("queries.model.post_create_test")

	var preCount, postCount int

	var preReceiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[string], value string) error {
		preCount++
		t.Log("Pre-Create fired. Emitting Post-Create...")

		// Emitting a different signal from within the listener
		return postCreate.Send(t.Context(), "triggered from pre-create")
	})

	var postReceiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[string], value string) error {
		postCount++
		t.Log("Post-Create fired.")
		return nil
	})

	preCreate.Connect(t.Context(), preReceiver)
	postCreate.Connect(t.Context(), postReceiver)

	// Fire the first signal
	var err = preCreate.Send(t.Context(), "initial trigger")
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
