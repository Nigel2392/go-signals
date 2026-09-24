package pkg_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/develop"
	"github.com/Nigel2392/go-signals/pkg/logger"
	"github.com/Nigel2392/go-signals/pkg/memory"
	"github.com/Nigel2392/go-signals/pkg/redis"
	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/Nigel2392/go-signals/pubsub2"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func connectSignal[T any](t testing.TB, amount int, signal signals.Signal[T], receiverFunc func(ctx context.Context, signal signals.Signal[T], value T) error) {
	t.Helper()

	for i := 0; i < amount; i++ {
		signal.Listen(context.Background(), receiverFunc)
	}
}

//	func drain(b testing.TB, c <-chan error) {
//		b.Helper()
//		var finished bool
//
//		if develop.DEVELOP {
//
//			if t, ok := b.(*testing.T); ok {
//
//				go func() {
//					<-time.After(5 * time.Second)
//
//					if finished {
//						return
//					}
//
//					t.Errorf("deadlock detected in test %q", t.Name())
//				}()
//			}
//		}
//
//		for err := range c {
//			b.Errorf("error from error channel: %v", err)
//		}
//
//		finished = true
//
//		if develop.DEVELOP {
//			if t, ok := b.(*testing.T); ok {
//				t.Log("channel drained")
//			}
//		}
//
//	}

type _pool interface {
	pkgName() string
	newPool(b testing.TB, client any, opts ...pubsub.PoolOption) pubsub.AbstractPool
	waitLoop(b testing.TB, wg *sync.WaitGroup, poolVal pubsub.AbstractPool)
	newSignal(b testing.TB, name string, poolVal pubsub.AbstractPool) signals.Signal[string]
	closePool(b testing.TB, poolVal pubsub.AbstractPool)
}

type abstractPool[T pubsub.AbstractPool] interface {
	pubsub.AbstractPool
}

type pool[T abstractPool[T]] struct {
	name   string
	_new   func(b testing.TB, client any, opts ...pubsub.PoolOption) T
	wait   func(b testing.TB, wg *sync.WaitGroup, pool T)
	signal func(b testing.TB, name string, pool T) signals.Signal[string]
	close  func(b testing.TB, pool T)
}

func (p pool[T]) pkgName() string {
	return p.name
}

func (p pool[T]) waitLoop(b testing.TB, wg *sync.WaitGroup, poolVal pubsub.AbstractPool) {
	p.wait(b, wg, poolVal.(T))
}

func (p pool[T]) newPool(b testing.TB, client any, opts ...pubsub.PoolOption) pubsub.AbstractPool {
	return p._new(b, client, opts...)
}

func (p pool[T]) newSignal(b testing.TB, name string, poolVal pubsub.AbstractPool) signals.Signal[string] {
	return p.signal(b, name, poolVal.(T))
}

func (p pool[T]) closePool(b testing.TB, poolVal pubsub.AbstractPool) {
	p.close(b, poolVal.(T))
}

type client struct {
	name string
	init func(b testing.TB, async bool) any
}

var clients = []client{
	{"Redis",
		func(b testing.TB, async bool) any {
			m := miniredis.NewMiniRedis()
			// err := m.StartAddr(fmt.Sprintf("127.0.0.1:%d", cnt.Load()))
			err := m.Start()
			if err != nil {
				b.Fatalf("could not instantiate redis server: %v", err)
			}

			b.Cleanup(func() {
				m.Close()
			})

			return redis.PubSub(async, goredis.NewClient(&goredis.Options{
				Addr: m.Addr(),
			}))
		},
	},
	{"Memory",
		func(b testing.TB, async bool) any { return memory.PubSub(async) },
	},
}

var pools = []_pool{
	pool[*pubsub.Pool[string]]{
		name: "PubSub",
		_new: func(b testing.TB, client any, opts ...pubsub.PoolOption) *pubsub.Pool[string] {
			return pubsub.New[string](b.Context(), client, opts...)
		},
		wait: func(b testing.TB, wg *sync.WaitGroup, pool *pubsub.Pool[string]) {
			for h, err := range pool.WaitLoop(b.Context()) {

				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {

						if ch := pool.Channel(b.Context()); cap(ch) > 0 && len(ch) > cap(ch)-3 {
							t.Errorf(
								"probable deadlock detected in test %q with pool channel len %d and cap %d",
								t.Name(), len(ch), cap(ch),
							)
						}

						t.Logf("processing handler in test %q: (handler: %t | err: %v)", t.Name(), h.BasePool == nil, err)
					}
				}

				if errors.Is(err, io.EOF) {
					return
				}

				// b.Log(v, err)
				if err != nil {
					b.Error(err)
					return
				}

				var finished bool

				if develop.DEVELOP {

					if t, ok := b.(*testing.T); ok {

						go func() {
							<-time.After(5 * time.Second)

							if finished {
								return
							}

							t.Errorf("deadlock detected in test %q", t.Name())
						}()
					}
				}

				for err := range h.Process(b.Context()) {
					b.Errorf("error from error channel: %v", err)
				}

				finished = true

				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Log("channel drained")
					}
				}

				wg.Done()

				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Log("processing done, waiting for next...")
					}
				}
			}
		},
		signal: func(b testing.TB, name string, pool *pubsub.Pool[string]) signals.Signal[string] {
			return pool.NewSignal(b.Context(), name)
		},
		close: func(b testing.TB, pool *pubsub.Pool[string]) {
			pool.Close()
		},
	},
	pool[*pubsub2.Pool]{
		name: "PubSub2",
		_new: func(b testing.TB, client any, opts ...pubsub.PoolOption) *pubsub2.Pool {
			return pubsub2.New(b.Context(), client, opts...)
		},
		wait: func(b testing.TB, wg *sync.WaitGroup, pool *pubsub2.Pool) {
			for h, err := range pool.WaitLoop(b.Context()) {
				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Logf("processing handler in test %q: (handler: %t | err: %v)", t.Name(), h.BasePool == nil, err)
					}
				}

				if errors.Is(err, io.EOF) {
					return
				}
				// b.Log(v, err)
				if err != nil {
					b.Error(err)
					return
				}

				var finished bool

				if develop.DEVELOP {

					if t, ok := b.(*testing.T); ok {

						go func() {
							<-time.After(5 * time.Second)

							if finished {
								return
							}

							t.Errorf("deadlock detected in test %q", t.Name())
						}()
					}
				}

				for err := range h.Process(b.Context()) {
					b.Errorf("error from error channel: %v", err)
				}

				finished = true

				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Log("channel drained")
					}
				}

				wg.Done()
			}
		},
		signal: func(b testing.TB, name string, pool *pubsub2.Pool) signals.Signal[string] {
			return pool.NewSignal[string](b.Context(), name)
		},
		close: func(b testing.TB, pool *pubsub2.Pool) {
			pool.Close()
		},
	},
	pool[*pubsub2.Pool]{
		name: "TPool",
		_new: func(b testing.TB, client any, opts ...pubsub.PoolOption) *pubsub2.Pool {
			return pubsub2.New(b.Context(), client, opts...)
		},
		wait: func(b testing.TB, wg *sync.WaitGroup, pool *pubsub2.Pool) {
			for h, err := range pool.TPool[string]().WaitLoop(b.Context()) {
				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Logf("processing handler in test %q: (handler: %t | err: %v)", t.Name(), h.BasePool == nil, err)
					}
				}

				if errors.Is(err, io.EOF) {
					return
				}
				// b.Log(v, err)
				if err != nil {

					b.Error(err)
					return
				}

				var finished bool
				if develop.DEVELOP {

					if t, ok := b.(*testing.T); ok {

						go func() {
							<-time.After(5 * time.Second)

							if finished {
								return
							}

							t.Errorf("deadlock detected in test %q", t.Name())
						}()
					}
				}

				for err := range h.Process(b.Context()) {
					b.Errorf("error from error channel: %v", err)
				}

				finished = true

				if develop.DEVELOP {
					if t, ok := b.(*testing.T); ok {
						t.Log("channel drained")
					}
				}

				wg.Done()
			}
		},
		signal: func(b testing.TB, name string, pool *pubsub2.Pool) signals.Signal[string] {
			return pool.TPool[string]().NewSignal(b.Context(), name)
		},
		close: func(b testing.TB, pool *pubsub2.Pool) {
			pool.Close()
		},
	},
}

func namedTest(bench string, extra ...string) string {
	var sb strings.Builder

	sb.WriteString(bench)

	for _, s := range extra {
		sb.WriteRune('/')
		sb.WriteString(s)
	}

	return sb.String()
}

var receiverAmounts = []int{TOTAL_AMOUNT / 8, TOTAL_AMOUNT / 4, TOTAL_AMOUNT / 2, TOTAL_AMOUNT}

type benchmark struct {
	name string
	list []string
	exec func(b *testing.B)
}

func BenchmarkPkg(b *testing.B) {
	if !testing.Verbose() {
		receiverAmounts = []int{TOTAL_AMOUNT}
	}

	var tests = make([]benchmark, 0, len(clients)*len(pools)*len(receiverAmounts)*3)
	for bench, pool := range iterTestables(b) {
		for _, TOTAL_AMOUNT := range receiverAmounts {

			bench := bench
			pool := pool
			TOTAL_AMOUNT := TOTAL_AMOUNT

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(TOTAL_AMOUNT)),
				list: []string{
					pool.pkgName(), "MaxSend", bench.name, "1", strconv.Itoa(TOTAL_AMOUNT),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, false),
						pubsub.PoolLog(logger.Null{}),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(b, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					var wg = new(sync.WaitGroup)
					go pool.waitLoop(b, wg, p)

					wg.Add(b.N)
					b.ResetTimer()

					for i := 0; i < b.N; i++ {
						err := signal.Send(b.Context(), "This is a signal message!")
						if err != nil {
							b.Error(err)
						}
					}

					wg.Wait()
					b.StopTimer()

					if int(incr.Load()) != (TOTAL_AMOUNT * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "Cycle", "ChanSelect", strconv.Itoa(TOTAL_AMOUNT)),
				list: []string{
					"Cycle", "ChanSelect", pool.pkgName(), bench.name, "0", strconv.Itoa(TOTAL_AMOUNT),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, false),
						pubsub.PoolLog(logger.Null{}),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(b, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					b.ResetTimer()

					for b.Loop() {
						err := signal.Send(b.Context(), "This is a signal message!")
						if err != nil {
							b.Error(err)
						}

						if err := p.Cycle(b.Context(), pubsub.CycleOptions{Flags: pubsub.CF_NO_RETRY}); err != nil {
							b.Errorf("expected no error, but got %v", err)
							return
						}
					}

					b.StopTimer()

					if int(incr.Load()) != (TOTAL_AMOUNT * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "Cycle", "TryLoop", strconv.Itoa(TOTAL_AMOUNT)),
				list: []string{
					"Cycle", "TryLoop", pool.pkgName(), bench.name, "0", strconv.Itoa(TOTAL_AMOUNT),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, true),
						pubsub.PoolLog(logger.Null{}),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(b, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					b.ResetTimer()

					for b.Loop() {
						err := signal.Send(b.Context(), "This is a signal message!")
						if err != nil {
							b.Error(err)
						}

						if err := p.Cycle(b.Context(), pubsub.CycleOptions{Flags: pubsub.CF_NO_RETRY}); err != nil {
							b.Errorf("expected no error, but got %v", err)
							return
						}

					}

					b.StopTimer()

					if int(incr.Load()) != (TOTAL_AMOUNT * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(TOTAL_AMOUNT), "Parallel"),
				list: []string{
					pool.pkgName(), "MaxSend", bench.name, "2", strconv.Itoa(TOTAL_AMOUNT),
				},
				exec: func(b *testing.B) {

					if testing.Verbose() {
						b.Log("running in parallel")
					}

					p := pool.newPool(
						b,
						bench.init(b, false),
						pubsub.PoolLog(logger.Null{}),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(b, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					b.StartTimer()

					var wg = new(sync.WaitGroup)
					go pool.waitLoop(b, wg, p)

					wg.Add(b.N)
					b.ResetTimer()

					b.RunParallel(func(p *testing.PB) {
						for p.Next() {
							err := signal.Send(b.Context(), "This is a signal message!")
							if err != nil {
								b.Error(err)
							}
						}
					})

					wg.Wait()
					b.StopTimer()

					if int(incr.Load()) != (TOTAL_AMOUNT * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "RoundTrip", strconv.Itoa(TOTAL_AMOUNT)),
				list: []string{
					pool.pkgName(), "RoundTrip", bench.name, "0", strconv.Itoa(TOTAL_AMOUNT),
				},
				exec: func(b *testing.B) {
					b.StopTimer()

					p := pool.newPool(
						b,
						bench.init(b, false),
						pubsub.PoolLog(logger.Null{}),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(b, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					var wg = new(sync.WaitGroup)
					go pool.waitLoop(b, wg, p)

					b.StartTimer()
					b.ResetTimer()

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

					if int(incr.Load()) != (TOTAL_AMOUNT * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * b.N))
					}

					pool.closePool(b, p)
				},
			})
		}
	}

	slices.SortStableFunc(tests, func(a, b benchmark) int { return slices.Compare(a.list, b.list) })

	for _, t := range tests {
		b.Run(t.name, t.exec)
	}
}

func iterTestables(t testing.TB) iter.Seq2[client, _pool] {
	t.Helper()

	return func(yield func(client, _pool) bool) {

		t.Helper()

		for _, client := range clients {
			for _, pool := range pools {
				if !yield(client, pool) {
					return
				}
			}
		}
	}
}

func asyncStr(b bool) string {
	if b {
		return "Async"
	}
	return "Sync"
}

func TestPkg(t *testing.T) {
	if !testing.Verbose() {
		receiverAmounts = []int{TOTAL_AMOUNT}
	}

	for _, async := range []bool{true, false} {
		for client, pool := range iterTestables(t) {

			t.Run(namedTest(pool.pkgName(), client.name, "TestNestedSignalsCrossTrigger", asyncStr(async)), func(t *testing.T) {

				p := pool.newPool(t, client.init(t, async))

				var incr = new(atomic.Int64)
				var signal = pool.newSignal(t, t.Name(), p)
				connectSignal(t, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
					incr.Add(1)
					return nil
				})

				// Simulating your exact ORM scenario
				var preCreate = pool.newSignal(t, "queries.model.pre_create_test", p)
				var postCreate = pool.newSignal(t, "queries.model.post_create_test", p)
				var preCount, postCount int

				preCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
					preCount++
					t.Log("Pre-Create fired. Emitting Post-Create...")
					// Emitting a different signal from within the listener
					return postCreate.Send(t.Context(), "triggered from pre-create")
				})

				postCreate.Listen(t.Context(), func(ctx context.Context, sig signals.Signal[string], value string) error {
					postCount++
					t.Log("Post-Create fired.")
					return nil
				})

				// Fire the first signal
				var err = preCreate.Send(t.Context(), "initial trigger")
				if err != nil {
					t.Fatalf("Failed to execute cross-trigger: %s", err.Error())
				}

				if err := p.Cycle(t.Context(), pubsub.CycleOptions{}); err != nil {
					t.Errorf("expected no error, but got %v", err)
					return
				}

				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
				defer cancel()

				if err := p.Cycle(ctx, pubsub.CycleOptions{}); err != nil && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("expected only context deadline error, but got %v", err)
					return
				}

				// Verify both fired exactly once
				if preCount != 1 {
					t.Errorf("Expected preCount to be 1, got %d", preCount)
				}
				if postCount != 1 {
					t.Errorf("Expected postCount to be 1, got %d", postCount)
				}

				pool.closePool(t, p)
			})

			// for _, async := range []bool{true, false} {
			t.Run(namedTest(pool.pkgName(), client.name, "TestMaxSendMany"), func(t *testing.T) {
				var (
					incr     = new(atomic.Int64)
					finished bool
				)

				go func() {
					<-t.Context().Done()
					if finished {
						return
					}

					t.Errorf(
						"deadlock detected in test %q, actual signals received: %d/%d",
						t.Name(), int(incr.Load())/TOTAL_AMOUNT, SEND_X_TIMES,
					)
				}()

				p := pool.newPool(
					t,
					client.init(t, false),
				)

				var signal = pool.newSignal(t, t.Name(), p)
				connectSignal(t, TOTAL_AMOUNT, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
					incr.Add(1)
					return nil
				})

				var wg = new(sync.WaitGroup)
				go pool.waitLoop(t, wg, p)

				wg.Add(SEND_X_TIMES)

				for i := 0; i < SEND_X_TIMES; i++ {
					err := signal.Send(t.Context(), "This is a signal message!")
					if err != nil {
						t.Fatalf("error during signal send: %v", err)
					}
				}

				t.Log("waiting...")

				wg.Wait()

				finished = true

				if int(incr.Load()) != (TOTAL_AMOUNT * SEND_X_TIMES) {
					t.Fatalf("counter does not match expected: %d != %d", incr.Load(), (TOTAL_AMOUNT * SEND_X_TIMES))
				}

				pool.closePool(t, p)
			})
			// }
		}
	}
}
