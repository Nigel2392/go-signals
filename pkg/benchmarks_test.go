package pkg_test

import (
	"context"
	"errors"
	"io"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

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
		var receiver = signals.NewRecv(receiverFunc)
		signal.Connect(context.Background(), receiver)
	}
}

type _pool interface {
	pkgName() string
	newPool(b *testing.B, client any, opts ...pubsub.PoolOption) any
	waitLoop(b *testing.B, wg *sync.WaitGroup, poolVal any)
	newSignal(b *testing.B, poolVal any) signals.Signal[string]
	closePool(b *testing.B, poolVal any)
}

type pool[T pubsub.ConfigErrPool[T]] struct {
	name   string
	_new   func(b *testing.B, client any, opts ...pubsub.PoolOption) T
	wait   func(b *testing.B, wg *sync.WaitGroup, pool T)
	signal func(b *testing.B, pool T) signals.Signal[string]
	close  func(b *testing.B, pool T)
}

func (p pool[T]) pkgName() string {
	return p.name
}

func (p pool[T]) waitLoop(b *testing.B, wg *sync.WaitGroup, poolVal any) {
	p.wait(b, wg, poolVal.(T))
}

func (p pool[T]) newPool(b *testing.B, client any, opts ...pubsub.PoolOption) any {
	return p._new(b, client, append(opts, pubsub.PoolOnError(func(ctx context.Context, p T, err error) {
		b.Log(string(debug.Stack()))
		b.Error(err)
	}))...)
}

func (p pool[T]) newSignal(b *testing.B, poolVal any) signals.Signal[string] {
	return p.signal(b, poolVal.(T))
}

func (p pool[T]) closePool(b *testing.B, poolVal any) {
	p.close(b, poolVal.(T))
}

type benchmark struct {
	name string
	init func(b *testing.B, async bool) any
}

var benchmarkSetup = []benchmark{
	{"Redis",
		func(b *testing.B, async bool) any {
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
		func(b *testing.B, async bool) any { return memory.PubSub(async) },
	},
}

var pools = []_pool{

	pool[*pubsub.Pool[string]]{
		name: "PubSub",
		_new: func(b *testing.B, client any, opts ...pubsub.PoolOption) *pubsub.Pool[string] {
			return pubsub.New[string](b.Context(), client, opts...)
		},
		wait: func(b *testing.B, wg *sync.WaitGroup, pool *pubsub.Pool[string]) {
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
		signal: func(b *testing.B, pool *pubsub.Pool[string]) signals.Signal[string] {
			return pool.NewSignal(b.Context(), uuid.New().String())
		},
		close: func(b *testing.B, pool *pubsub.Pool[string]) {
			pool.Close()
		},
	},
	pool[*pubsub2.Pool]{
		name: "PubSub2",
		_new: func(b *testing.B, client any, opts ...pubsub.PoolOption) *pubsub2.Pool {
			return pubsub2.New(b.Context(), client, opts...)
		},
		wait: func(b *testing.B, wg *sync.WaitGroup, pool *pubsub2.Pool) {
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
		signal: func(b *testing.B, pool *pubsub2.Pool) signals.Signal[string] {
			return pool.NewSignal[string](b.Context(), uuid.New().String())
		},
		close: func(b *testing.B, pool *pubsub2.Pool) {
			pool.Close()
		},
	},
	pool[*pubsub2.Pool]{
		name: "TPool",
		_new: func(b *testing.B, client any, opts ...pubsub.PoolOption) *pubsub2.Pool {
			return pubsub2.New(b.Context(), client, opts...)
		},
		wait: func(b *testing.B, wg *sync.WaitGroup, pool *pubsub2.Pool) {
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
		signal: func(b *testing.B, pool *pubsub2.Pool) signals.Signal[string] {
			return pool.TPool[string]().NewSignal(b.Context(), uuid.New().String())
		},
		close: func(b *testing.B, pool *pubsub2.Pool) {
			pool.Close()
		},
	},
}

func benchName(bench string, extra ...string) string {
	var sb strings.Builder

	sb.WriteString(bench)

	for _, s := range extra {
		sb.WriteRune('/')
		sb.WriteString(s)
	}

	return sb.String()
}

var receiverAmounts = []int{totalReceivers / 8, totalReceivers / 4, totalReceivers / 2, totalReceivers}

type test struct {
	name string
	list []string
	exec func(b *testing.B)
}

func BenchmarkPkg(b *testing.B) {
	if !testing.Verbose() {
		receiverAmounts = []int{totalReceivers}
	}

	var tests = make([]test, 0, len(benchmarkSetup)*len(pools)*len(receiverAmounts)*3)
	for _, bench := range benchmarkSetup {
		for _, pool := range pools {
			for _, totalReceivers := range receiverAmounts {

				bench := bench
				pool := pool
				totalReceivers := totalReceivers

				tests = append(tests, test{
					name: benchName(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(totalReceivers)),
					list: []string{
						pool.pkgName(), "MaxSend", bench.name, "1", strconv.Itoa(totalReceivers),
					},
					exec: func(b *testing.B) {
						p := pool.newPool(
							b,
							bench.init(b, false),
						)

						var incr = new(atomic.Int64)
						var signal = pool.newSignal(b, p)
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

				tests = append(tests, test{
					name: benchName(pool.pkgName(), bench.name, "MaxSend", strconv.Itoa(totalReceivers), "Parallel"),
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
						var signal = pool.newSignal(b, p)
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

				tests = append(tests, test{
					name: benchName(pool.pkgName(), bench.name, "RoundTrip", strconv.Itoa(totalReceivers)),
					list: []string{
						pool.pkgName(), "RoundTrip", bench.name, "3", strconv.Itoa(totalReceivers),
					},
					exec: func(b *testing.B) {
						b.StopTimer()

						p := pool.newPool(
							b,
							bench.init(b, false),
						)

						var incr = new(atomic.Int64)
						var signal = pool.newSignal(b, p)
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
	}

	slices.SortStableFunc(tests, func(a, b test) int { return slices.Compare(a.list, b.list) })

	for _, t := range tests {
		b.Run(t.name, t.exec)
	}
}
