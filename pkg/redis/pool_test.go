package redis

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

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

func TestPoolSend(t *testing.T) {
	c, err := miniredis.Run()
	if err != nil {
		t.Fatalf("could not instantiate redis server: %v", err)
	}

	t.Cleanup(c.Close)

	redisPool := NewPool[MyType](redis.NewClient(&redis.Options{
		Addr: c.Addr(),
	}))

	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	go redisPool.Loop(t.Context(), time.Millisecond*10, errCh, exitCh)

	var mu = new(sync.Mutex)
	var typeList []MyType
	test1 := redisPool.NewSignal(t.Context(), "test-pool-channel-1")
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
	test2 := redisPool.NewSignal(t.Context(), "test-pool-channel-2")
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
	test3 := redisPool.NewSignal(t.Context(), "test-pool-channel-1")
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

	err = test1.Send(t.Context(), MyType{
		ID:   uuid.Max,
		Name: "MyTypeName",
	})
	if err != nil {
		t.Fatalf("could not send signal: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if len(typeList) != 4 {
		t.Fatalf("Expected 4 items in typeList, got %d: %v", len(typeList), typeList)
	}

	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatal(err)
		}
	default:
	}

	close(errCh)
	close(exitCh)
}

func TestNestedSignals_CrossTrigger(t *testing.T) {
	c, err := miniredis.Run()
	if err != nil {
		t.Fatalf("could not instantiate redis server: %v", err)
	}

	t.Cleanup(c.Close)

	pool := NewPool[string](redis.NewClient(&redis.Options{
		Addr: c.Addr(),
	}))

	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	go pool.Loop(t.Context(), time.Millisecond*10, errCh, exitCh)

	var preCreate = pool.NewSignal(t.Context(), "queries.model.pre_create_test")
	var postCreate = pool.NewSignal(t.Context(), "queries.model.post_create_test")

	var preCount, postCount int

	var _, _ = preCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
		preCount++
		t.Log("Pre-Create fired. Emitting Post-Create...")

		// Emitting a different signal from within the listener
		return postCreate.Send(t.Context(), "triggered from pre-create")
	})

	var _, _ = postCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
		postCount++
		t.Log("Post-Create fired.")
		return nil
	})

	// Fire the first signal
	err = preCreate.Send(t.Context(), "initial trigger")
	if err != nil {
		t.Fatalf("Failed to execute cross-trigger: %s", err.Error())
	}

	time.Sleep(50 * time.Millisecond)

	// Verify both fired exactly once
	if preCount != 1 {
		t.Errorf("Expected preCount to be 1, got %d", preCount)
	}
	if postCount != 1 {
		t.Errorf("Expected postCount to be 1, got %d", postCount)
	}
	select {
	case err, ok := <-errCh:
		if ok {
			t.Fatal(err)
		}
	default:
	}

	close(errCh)
	close(exitCh)
}

func TestSendAsync(t *testing.T) {
	c, err := miniredis.Run()
	if err != nil {
		t.Fatalf("could not instantiate redis server: %v", err)
	}

	t.Cleanup(c.Close)

	pool := NewPool[string](redis.NewClient(&redis.Options{
		Addr: c.Addr(),
	}))

	var (
		errCh  = make(chan error, 10)
		exitCh = make(chan struct{}, 1)
	)

	go pool.Loop(t.Context(), time.Millisecond*10, errCh, exitCh)

	var signal = pool.NewSignal(t.Context(), strconv.Itoa(int(time.Now().UnixNano())))
	var totalReceivers = 32000

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

	close(errCh)
	close(exitCh)

}
