package pkg_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pkg/memory"
	"github.com/Nigel2392/go-signals/pkg/redis"
	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/Nigel2392/go-signals/pubsub2"
	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

var totalReceivers = 32000

func connectSignal[T any](amount int, signal signals.Signal[T], receiverFunc func(ctx context.Context, signal signals.Signal[T], value T) error) {
	for i := 0; i < amount; i++ {
		signal.Listen(context.Background(), receiverFunc)
	}
}

type _pool interface {
	pkgName() string
	newPool(b testing.TB, client any, opts ...pubsub.PoolOption) pubsub.AbstractPool
	waitLoop(b testing.TB, wg *sync.WaitGroup, poolVal pubsub.AbstractPool)
	newSignal(b testing.TB, name string, poolVal pubsub.AbstractPool) signals.Signal[string]
	closePool(b testing.TB, poolVal pubsub.AbstractPool)
}

type abstractPool[T pubsub.ConfigErrPool[T]] interface {
	pubsub.AbstractPool
	pubsub.ConfigErrPool[T]
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
	return p._new(b, client, append(opts, pubsub.PoolOnError(func(ctx context.Context, p T, err error) {
		b.Log(string(debug.Stack()))
		b.Error(err)
	}))...)
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
				if errors.Is(err, io.EOF) {
					return
				}

				// b.Log(v, err)
				if err != nil {
					b.Error(err)
					return
				}

				h.Process(b.Context())
				wg.Done()
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
				if errors.Is(err, io.EOF) {
					return
				}
				// b.Log(v, err)
				if err != nil {
					b.Error(err)
					return
				}
				h.Process(b.Context())
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
				if errors.Is(err, io.EOF) {
					return
				}
				// b.Log(v, err)
				if err != nil {
					b.Error(err)
					return
				}
				h.Process(b.Context())
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

var receiverAmounts = []int{totalReceivers / 8, totalReceivers / 4, totalReceivers / 2, totalReceivers}

type benchmark struct {
	name string
	list []string
	exec func(b *testing.B)
}

func BenchmarkPkg(b *testing.B) {
	if !testing.Verbose() {
		receiverAmounts = []int{totalReceivers}
	}

	var tests = make([]benchmark, 0, len(clients)*len(pools)*len(receiverAmounts)*3)
	for bench, pool := range iterTestables(b) {
		for _, totalReceivers := range receiverAmounts {

			bench := bench
			pool := pool
			totalReceivers := totalReceivers

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(totalReceivers)),
				list: []string{
					pool.pkgName(), "MaxSend", bench.name, "1", strconv.Itoa(totalReceivers),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, false),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
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

					if int(incr.Load()) != (totalReceivers * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "Cycle", "Sync", strconv.Itoa(totalReceivers)),
				list: []string{
					"Cycle", "Sync", pool.pkgName(), bench.name, "0", strconv.Itoa(totalReceivers),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, false),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					b.ResetTimer()

					for b.Loop() {
						err := signal.Send(b.Context(), "This is a signal message!")
						if err != nil {
							b.Error(err)
						}

						err = p.Cycle(b.Context(), false)
						if err != nil {
							b.Error(err)
						}
					}

					b.StopTimer()

					if int(incr.Load()) != (totalReceivers * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "Cycle", "Async", strconv.Itoa(totalReceivers)),
				list: []string{
					"Cycle", "Async", pool.pkgName(), bench.name, "0", strconv.Itoa(totalReceivers),
				},
				exec: func(b *testing.B) {
					p := pool.newPool(
						b,
						bench.init(b, true),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
						incr.Add(1)
						return nil
					})

					b.ResetTimer()

					for b.Loop() {
						err := signal.Send(b.Context(), "This is a signal message!")
						if err != nil {
							b.Error(err)
						}

						err = p.Cycle(b.Context(), false)
						if err != nil {
							b.Error(err)
						}
					}

					b.StopTimer()

					if int(incr.Load()) != (totalReceivers * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(totalReceivers), "Parallel"),
				list: []string{
					pool.pkgName(), "MaxSend", bench.name, "2", strconv.Itoa(totalReceivers),
				},
				exec: func(b *testing.B) {

					if testing.Verbose() {
						b.Log("running in parallel")
					}

					p := pool.newPool(
						b,
						bench.init(b, false),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
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

					if int(incr.Load()) != (totalReceivers * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
					}

					pool.closePool(b, p)
				},
			})

			tests = append(tests, benchmark{
				name: namedTest(pool.pkgName(), bench.name, "RoundTrip", strconv.Itoa(totalReceivers)),
				list: []string{
					pool.pkgName(), "RoundTrip", bench.name, "0", strconv.Itoa(totalReceivers),
				},
				exec: func(b *testing.B) {
					b.StopTimer()

					p := pool.newPool(
						b,
						bench.init(b, false),
					)

					var incr = new(atomic.Int64)
					var signal = pool.newSignal(b, b.Name(), p)
					connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
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

					if int(incr.Load()) != (totalReceivers * b.N) {
						b.Fatalf("counter does not match expected: %d != %d", incr.Load(), (totalReceivers * b.N))
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

func iterTestables(_ testing.TB) iter.Seq2[client, _pool] {
	return func(yield func(client, _pool) bool) {
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
		receiverAmounts = []int{totalReceivers}
	}

	for _, async := range []bool{true, false} {
		for client, pool := range iterTestables(t) {

			t.Run(namedTest(pool.pkgName(), client.name, "TestNestedSignalsCrossTrigger", asyncStr(async)), func(t *testing.T) {

				p := pool.newPool(t, client.init(t, async))

				var incr = new(atomic.Int64)
				var signal = pool.newSignal(t, t.Name(), p)
				connectSignal(totalReceivers, signal, func(ctx context.Context, signal signals.Signal[string], value string) error {
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

				if err := p.Cycle(t.Context(), false); err != nil {
					t.Fatalf("error during cycle: %v", err)
				}

				if err := p.Cycle(t.Context(), false); err != nil {
					t.Fatalf("error during cycle: %v", err)
				}

				ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
				defer cancel()

				if err := p.Cycle(ctx, false); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("error during cycle: %v", err)
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

		}
	}
}
