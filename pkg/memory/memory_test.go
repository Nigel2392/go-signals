package memory

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/google/uuid"
)

var totalReceivers = 32000

func connectSignal[T any](amount int, signal signals.Signal[T], receiverFunc func(ctx context.Context, signal signals.Signal[T], value T) error) {
	for i := 0; i < amount; i++ {
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(context.Background(), receiver)
	}
}

type MyType struct {
	ID   uuid.UUID
	Name string
}

//
//	func BenchmarkSignalsBlocking(b *testing.B) {
//		b.StopTimer()
//
//		pool := pubsub.New[string](
//			PubSub(),
//			// pubsub.PoolTickTime[string](time.Millisecond/5),
//			pubsub.PoolPrefersBlock,
//			pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
//				b.Log(string(debug.Stack()))
//				b.Error(err)
//			}),
//		)
//
//		var incr *atomic.Int64
//		var signal = pool.NewSignal(b.Context(), strconv.Itoa(int(time.Now().UnixNano())))
//
//		connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
//			incr.Add(1)
//			return nil
//		})
//
//		b.StartTimer()
//
//		for b.Loop() {
//			incr = new(atomic.Int64)
//
//			err := signal.Send(b.Context(), "This is a signal message!")
//			if err != nil {
//				b.Error(err)
//			}
//
//			if int(incr.Load()) != totalReceivers {
//				b.Fatalf("counter does not match expected: %d != %d", incr.Load(), totalReceivers)
//			}
//		}
//	}
//
//	func BenchmarkSignalsTrying(b *testing.B) {
//		b.StopTimer()
//
//		pool := pubsub.New[string](
//			PubSub(),
//			pubsub.PoolTickTime[string](time.Millisecond/5),
//			// pubsub.PoolPrefersBlock,
//			pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
//				b.Log(string(debug.Stack()))
//				b.Error(err)
//			}),
//		)
//
//		var incr = new(atomic.Int64)
//		var wg = new(sync.WaitGroup)
//		wg.Add(totalReceivers * b.N)
//
//		var signal = pool.NewSignal(b.Context(), strconv.Itoa(int(time.Now().UnixNano())))
//		connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
//			wg.Done()
//			return nil
//		})
//
//		b.StartTimer()
//
//		for b.Loop() {
//
//			err := signal.Send(b.Context(), "This is a signal message!")
//			if err != nil {
//				b.Error(err)
//			}
//
//			pool.Work(b.Context())
//		}
//
//		wg.Wait()
//
//		if int(incr.Load()) != (totalReceivers * b.N) {
//			b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
//		}
//
//		pool.Close()
//	}

func TestPoolSend(t *testing.T) {

	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	pool := pubsub.New[MyType](
		PubSub(),
		pubsub.PoolTickTime[MyType](time.Millisecond*10),
		// pubsub.PoolPrefersBlock,
		pubsub.PoolOnError(func(p *pubsub.Pool[MyType], err error) {
			errCh <- err
		}),
	)

	go pool.Loop(t.Context())

	var mu = new(sync.Mutex)
	var typeList []MyType
	test1 := pool.NewSignal(t.Context(), "test-pool-channel-1")
	test1.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("INITIAL: Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})

	test1.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("SECOND:  Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})

	// These shouldnt activate
	test2 := pool.NewSignal(t.Context(), "test-pool-channel-2")
	test2.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("INITIAL: Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})
	test2.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("SECOND:  Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})

	// These SHOULD activate
	test3 := pool.NewSignal(t.Context(), "test-pool-channel-1")
	test3.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		mt.ID = uuid.New()
		t.Logf("THIRD:   Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})
	test3.Listen(t.Context(), func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		mt.ID = uuid.New()
		t.Logf("FOURTH:  Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})

	err := test1.Send(t.Context(), MyType{
		ID:   uuid.Max,
		Name: "MyTypeName",
	})
	if err != nil {
		t.Fatalf("could not send signal: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()

	if len(typeList) != 4 {
		t.Errorf("Expected 4 items in typeList, got %d: %v", len(typeList), typeList)
	}

	mu.Unlock()

	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatal(err)
		}
	default:
	}

	close(exitCh)
}

func TestPoolContextErr(t *testing.T) {

	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	pool := pubsub.New[MyType](
		PubSub(),
		pubsub.PoolTickTime[MyType](time.Millisecond*10),
		pubsub.PoolOnError(func(p *pubsub.Pool[MyType], err error) {
			errCh <- err
		}),
	)

	var ctx, cancel = context.WithCancel(context.Background())

	go pool.Loop(ctx)

	var mu = new(sync.Mutex)
	var typeList []MyType
	test1 := pool.NewSignal(ctx, "test-pool-channel-1")
	test1.Listen(ctx, func(ctx context.Context, s signals.Signal[MyType], mt MyType) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("INITIAL: Received: %T %v", mt, mt)
		typeList = append(typeList, mt)
		return nil
	})

	cancel()

	err := test1.Send(t.Context(), MyType{
		ID:   uuid.Max,
		Name: "MyTypeName",
	})
	if err != nil {
		t.Errorf("could not send signal: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(typeList) != 0 {
		t.Errorf("Expected 0 items in typeList, got %d: %v", len(typeList), typeList)
	}
	mu.Unlock()

	select {
	case err, ok := <-errCh:
		if !ok {
			t.Fatal("expected error, got none")
			break
		}

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected error %q, got %q", context.Canceled, err)
			break
		}

		t.Logf("[success] received EXPECTED error: %v", err)
	default:
	}

	close(exitCh)
}

func TestNestedSignals_CrossTrigger(t *testing.T) {
	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	pool := pubsub.New[string](
		PubSub(),
		pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
			errCh <- err
		}),
	)

	go pool.Loop(t.Context())

	var preCreate = pool.NewSignal(t.Context(), "queries.model.pre_create_test")
	var postCreate = pool.NewSignal(t.Context(), "queries.model.post_create_test")

	var preCount, postCount atomic.Int64

	var _, _ = preCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
		preCount.Add(1)
		t.Log("Pre-Create fired. Emitting Post-Create...")

		// Emitting a different signal from within the listener
		return postCreate.Send(t.Context(), "triggered from pre-create")
	})

	var _, _ = postCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
		postCount.Add(1)
		t.Log("Post-Create fired.")
		return nil
	})

	// Fire the first signal
	err := preCreate.Send(t.Context(), "initial trigger")
	if err != nil {
		t.Errorf("Failed to execute cross-trigger: %s", err.Error())
	}

	time.Sleep(50 * time.Millisecond)

	// Verify both fired exactly once
	if preCount.Load() != 1 {
		t.Errorf("Expected preCount to be 1, got %d", preCount.Load())
	}
	if postCount.Load() != 1 {
		t.Errorf("Expected postCount to be 1, got %d", postCount.Load())
	}
	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatal(err)
		}
	default:
	}

	close(exitCh)
}

func TestNestedSignals_SameSignal(t *testing.T) {
	var maxCalls int64 = 30

	var (
		errCh = make(chan error, 10)
	)

	pool := pubsub.New(
		PubSub(),
		// pubsub.PoolTickTime[string](time.Millisecond/5),
		pubsub.PoolTickTime[string](time.Millisecond/5),
		pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
			errCh <- err
		}),
	)

	go pool.Loop(t.Context())

	t.Cleanup(func() {
		// pool.Close()
	})

	var (
		callCount atomic.Int64
		signalID  = strconv.Itoa(int(time.Now().UnixNano()))
		signal    = pool.NewSignal(t.Context(), signalID)
	)

	var receiver = signals.NewRecv(func(ctx context.Context, sig signals.Signal[string], value string) error {
		callCount.Add(1)
		i := callCount.Load()
		t.Logf("Call %d: received %s", i, value)

		// Break condition to prevent infinite recursion / stack overflow
		if i < maxCalls {
			// Fire the exact same signal while currently inside its listener
			return sig.Send(t.Context(), "nested call")
		}
		return nil
	})

	signal.Connect(t.Context(), receiver)

	// If the mutex is not released before iterating listeners, this will deadlock instantly.
	err := signal.Send(t.Context(), "initial call")
	if err != nil {
		t.Errorf("Expected no errors during nested sends, got: %s", err.Error())
	}

	time.Sleep(50 * time.Millisecond)

	if callCount.Load() != maxCalls {
		t.Errorf("Expected %d calls, got %d", maxCalls, callCount.Load())
	}

	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatal(err)
		}
	default:
	}
}

func TestSendAsync(t *testing.T) {
	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	pool := pubsub.New(
		PubSub(),
		pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
			errCh <- err
		}),
	)

	go pool.Loop(t.Context())

	var signal = pool.NewSignal(t.Context(), strconv.Itoa(int(time.Now().UnixNano())))

	connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error { return errors.New(value) })

	var errChan chan error = signal.SendAsync(t.Context(), "This is a signal message!")
	var errs []error = make([]error, 0)
	for err := range errChan {
		if err != nil {
			errs = append(errs, err)
		}
	}

	time.Sleep(50 * time.Millisecond)

	select {
	case _, ok := <-errCh:
		if !ok {
			t.Fatal("expected error, got none")
		}
	default:
	}

	close(exitCh)
}
