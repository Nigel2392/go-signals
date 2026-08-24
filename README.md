# Go-Signals

A type-safe package for sending signals application-wide in Go.

Signals are a way to communicate between different parts of your application.

**Signals can also be used for communication across the network.**

Each subpackage is tested against race conditions and benchmarked to measure performance.

## Installation

```bash
go get github.com/Nigel2392/go-signals
```

## Core package

All signal methods accept a `context.Context` as the first argument.

The base signals package provides a [zero-allocation solution](#benchmark-results) for the Send/Receive lifecycle.

### Signal pool

All pools should implement at least the following interface:

```go
type SignalPool[T any] interface {
    NewSignal(ctx context.Context, name string) Signal[T]
    Send(ctx context.Context, name string, value T) error
}
```

```go
import "github.com/Nigel2392/go-signals"

// Create a typed pool (string payloads here).
var pool = signals.NewPool[string]()

// Get (or create) a named signal from the pool.
var signal = pool.Get("mysignal") // or pool.NewSignal(ctx, "mysignal")
```

### Connecting receivers and sending

```go
// Create a receiver.
var receiver = signals.NewRecv(func(ctx context.Context, s signals.Signal[string], value string) error {
    fmt.Println("received:", value)
    return nil
})

// Connect one or more receivers.
signal.Connect(context.Background(), receiver)

// Send, all connected receivers are called in order.
if err := signal.Send(context.Background(), "hello!"); err != nil {
    // individual receiver errors are wrapped in a SignalError
    if sigErr, ok := signals.SignalError(err); ok {
        fmt.Println("receivers that errored:", sigErr.Len())
    }
}

// Disconnect a receiver.
signal.Disconnect(context.Background(), receiver)

// Clear all receivers.
signal.Clear(context.Background())
```

### Convenience: `Listen`

Listen is provided as a convenience function to reduce boilerplate.

```go
// Creates and connects a receiver in a single call.
// Returns the receiver so it can be disconnected later.
recv, err := signal.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], value string) error {
    fmt.Println("received:", value)
    return nil
})
```

### Sending asynchronously

```go
// Each receiver is called in its own goroutine.
// Returns nil if there are no receivers.
// This behaviour differs from pubsub package signals' SendAsync function.
errChan := signal.SendAsync(context.Background(), "hello!")
if errChan != nil {
    for err := range errChan {
        if err != nil {
            fmt.Println("error:", err)
        }
    }
}
```

---

### Global pool

A package-level pool of `any`-typed signals is provided for convenience:

```go
// Get a signal from the global pool.
sig := signals.Get("myapp.event")

// Send via the global helper.
signals.Send(context.Background(), "myapp.event", "payload")

// Listen via the global helper.
signals.Listen(context.Background(), "myapp.event", func(ctx context.Context, s signals.Signal[any], v any) error {
    fmt.Println(v)
    return nil
})
```

### Pool helpers

These only apply to the pool in the `signals` package itself.

The pool in the `pubsub` package implements different methods.

```go
var pool = signals.NewPool[string]()

// Send to a named signal in the pool.
pool.Send(ctx, "mysignal", "value")

// Broadcast to every signal in the pool.
pool.SendGlobal(ctx, "value")

// Register a listener directly on the pool (creates the signal if absent).
pool.Listen(ctx, "mysignal", func(ctx context.Context, s signals.Signal[string], v string) error {
    fmt.Println(v)
    return nil
})

// Check existence without creating.
pool.Exists("mysignal")

// Remove a signal from the pool.
pool.Delete("mysignal")
```

---

## Pub/Sub pool

**`pubsub`**

The `pubsub` sub-package wraps any `PubSub` transport backend in a typed `Pool`.
Values are serialized with a pluggable `Encoder` (JSON by default) before being published.

### Interfaces

```go
// Custom encoders can be provided to easily serialize and deserialize
// data across the pubsub signal pool's publishing lifecycle
type Encoder = encoder.Encoder

// The PubSubPool is the interface that [Pool] implements.
//
// It allows for easily implementing and using a subscribe- publish pattern.
//
// Current backends for this functionality are implemented in:
//
// * `github.com/Nigel2392/go-signals/pkg/memory`
// * `github.com/Nigel2392/go-signals/pkg/redis`
type PubSubPool[T any] interface {
    signals.SignalPool[T]
    ChannelBinder

    // Loop is optimized to run in a separate goroutine, called by `go pool.Loop(ctx)`
    Loop(ctx context.Context)

    // WaitLoop is optimized to aggregate all central signals into the [PubSubPool]'s datachannel,
    // allowing for a lot more flexibility when it comes to testing and handling received data.
    WaitLoop(ctx context.Context, work bool) iter.Seq2[*Handler[T], error]

    // Send data across the pool for a topic to use.
    //
    // This method is also called by the [signal] type returned
    // from the pool's [PubSubPool.NewSignal] method.
    Send(ctx context.Context, topic string, value T) error

    // Stop all loops and close the pool down so no further processing can occur.
    Close()
}

// PubSub is the publisher backend used inside of the [PubSubPool]
//
// Implementations of PubSub are also allowed to implement [PubSubBinder],
// allowing direct access to the underlying data channel.
//
// Current uses of the data channel can be found in
// [memory_test.go/BenchmarkSignals] and [redis_test.go/BenchmarkSignals]
type PubSub interface {
    Publish(ctx context.Context, topic string, data []byte) error
    Subscribe(ctx context.Context, topic string) (Subscriber, error)
}

// Bind a [PubSub] to a [Pool] type.
type PubSubBinder interface {
    BindChannel(ChannelBinder)
}

// Subscribers are returned by the [PubSub] interface, these subscribers are
// used to retrieve data to send to the receiver objects.
type Subscriber interface {
    Close() error
    // TryReceive attempts a non-blocking read.
    // Returns (payload, true) if a message is immediately available.
    // Returns (nil, false) if the queue is empty.
    TryReceive() ([]byte, bool)
}

// Messages transmitted internally across the [ChannelBinder]'s data channel.
//
// These can also be used by [PubSub] backends to allow for [PubSubPool.WaitLoop] functionality.
type Message struct {
    Channel string
    Data    []byte
}

// ChannelBinder is implemented by the [Pool] type to
// allow for the blocking WaitLoop function.
type ChannelBinder interface {
    Client() PubSub
    Channel() chan *Message
    SetChannel(ch chan *Message)
}
```

### Polling loop mode

**(`async = true`)**

Use this when you don't want to create a new goroutine for each signal created by the pool.

This argument is also implemented for the redis `PubSub` backend.

```go
import (
    "github.com/Nigel2392/go-signals/pubsub"
    "github.com/Nigel2392/go-signals/pkg/memory"
)

backend := memory.PubSub(true) // async = true -> polling mode

pool := pubsub.New[string](backend,
    pubsub.PoolTickTime[string](time.Millisecond),
    pubsub.PoolOnError(func(p *pubsub.Pool[string], err error) {
        log.Println("pubsub error:", err)
    }),
)

// Create a named signal backed by the pubsub transport.
sig := pool.NewSignal(context.Background(), "chat.messages")

// Listen same interface as core signals.
sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[string], value string) error {
    fmt.Println("received:", value)
    return nil
})

// Start the polling loop (blocks until closed or ctx is cancelled).
go pool.Loop(context.Background())

// Publish encodes the value and sends it over the transport.
pool.Send(context.Background(), "chat.messages", "hello world")

// Stop the loop.
pool.Close()
```

### Channel / wait-loop mode

**(`async = false`)**

Use this when you want to drive dispatch yourself, or when the backend can forward
messages over a shared Go channel (e.g. for benchmarks or synchronous tests).

```go
backend := memory.PubSub(false) // async = false -> binds a shared channel
pool    := pubsub.New[string](backend)

// WaitLoop blocks, reading from the shared channel.
// work=true  -> decodes each message and dispatches it to receivers automatically.
// work=false -> yields raw Handler values for you to process.
for handler, err := range pool.WaitLoop(context.Background(), true) {
    if err != nil {
        log.Println(err)
        break
    }
    _ = handler // handler.Value, handler.Signal, handler.Receivers
}
```

### Pool options

| Option | Description |
| --- | --- |
| `pubsub.PoolEncoder[T](enc)` | Override the default JSON encoder |
| `pubsub.PoolTickTime[T](d)` | Polling interval for `Loop` (default `500µs`) |
| `pubsub.PoolOnError[T](fn)` | Called when a receiver or decode fails |

## Backends

### In-memory

**(`pkg/memory`)**

Zero external dependencies. Suitable for single-process use.

If you know your program will always be a single process, and never distributed across a network
then you should use the regular `signals` package for performance benefits.

```go
import "github.com/Nigel2392/go-signals/pkg/memory"

// async=true  -> polling loop (pool.Loop)
// async=false -> channel mode (pool.WaitLoop)
backend := memory.PubSub(true)
pool    := pubsub.New[MyType](backend)
```

### Redis

**(`pkg/redis`)**

Backed by `github.com/redis/go-redis/v9`. Suitable for distributed / multi-process use.

```go
import (
    goredis "github.com/redis/go-redis/v9"
    "github.com/Nigel2392/go-signals/pkg/redis"
    "github.com/Nigel2392/go-signals/pubsub"
)

client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})

// async=true  -> polling loop (pool.Loop)
// async=false -> channel mode (pool.WaitLoop)
backend := redis.PubSub(true, client)
pool    := pubsub.New[MyType](backend,
    pubsub.PoolTickTime[MyType](10*time.Millisecond),
    pubsub.PoolOnError(func(p *pubsub.Pool[MyType], err error) {
        log.Println(err)
    }),
)

sig := pool.NewSignal(context.Background(), "my-topic")
sig.Listen(context.Background(), func(ctx context.Context, s signals.Signal[MyType], v MyType) error {
    fmt.Println("received:", v)
    return nil
})

go pool.Loop(context.Background())
pool.Send(context.Background(), "my-topic", MyType{Name: "hello"})
```

## Benchmark Results

Each benchmark is ran with 32000 no-op receivers provided to the signal, **meaning *each* iteration of the benchmark calls 32000 functions.**

This ensures the core framework is tested, instead of random receiver functionality.

The following command is used to run the benchmarks:

`go test -timeout="4s" -benchmem -run=^$ -benchtime=5s -count 5 -bench "^*$" -v .\...`

### `go-signals`

Benchmarks that use the base implementation are by far the fastest, as the base package implements
a zero- allocation solution without any data serialization for `Send` and `Receive`.

```sh
goos: windows
goarch: amd64
pkg: github.com/Nigel2392/go-signals
cpu: AMD Ryzen 7 5800H with Radeon Graphics
BenchmarkSignals
BenchmarkSignals-16        49615            118798 ns/op              10 B/op          0 allocs/op
BenchmarkSignals-16        50179            118337 ns/op              10 B/op          0 allocs/op
BenchmarkSignals-16        51232            117259 ns/op              10 B/op          0 allocs/op
BenchmarkSignals-16        50878            117753 ns/op              10 B/op          0 allocs/op
BenchmarkSignals-16        50912            117317 ns/op              10 B/op          0 allocs/op
```

### `pkg/memory`

The memory implementation of the `PubSub` interface should only be used for testing or development.

This is not due to any known bugs or logic- issues, it is purely because **the base go-signals package provides a very large performance benefit**.

*Meaning:* if you never expect to actually use a custom distributed backend, the performance penalty incurred by
many different select statements, channel interactions and serialization can be completely mitigated by using the base package.

```sh
goos: windows
goarch: amd64
pkg: github.com/Nigel2392/go-signals/pkg/memory
cpu: AMD Ryzen 7 5800H with Radeon Graphics
BenchmarkSignals
BenchmarkSignals-16        30634            194134 ns/op             628 B/op          9 allocs/op
BenchmarkSignals-16        32846            180649 ns/op             625 B/op          9 allocs/op
BenchmarkSignals-16        33552            180537 ns/op             624 B/op          9 allocs/op
BenchmarkSignals-16        32474            180551 ns/op             625 B/op          9 allocs/op
BenchmarkSignals-16        33363            178903 ns/op             624 B/op          9 allocs/op
```

### `pkg/redis`

The redis `PubSub` backend provides the most flexibility, allowing to share signals across different machines.

Each signals' value that gets published will get received by all receivers registered to that signal, this can even be across machines.

In this case, the `Signal` name is used as the topic for the `Subscribe/Publish` methods of the redis client.

Each applications' pool will only receive the value from redis exactly once, and then distribute that value across all registered receivers.

```sh
goos: windows
goarch: amd64
pkg: github.com/Nigel2392/go-signals/pkg/redis
cpu: AMD Ryzen 7 5800H with Radeon Graphics
BenchmarkSignals
BenchmarkSignals-16        22947            259218 ns/op            1519 B/op         41 allocs/op
BenchmarkSignals-16        23269            257271 ns/op            1510 B/op         41 allocs/op
BenchmarkSignals-16        23178            258278 ns/op            1508 B/op         41 allocs/op
BenchmarkSignals-16        23119            258837 ns/op            1508 B/op         41 allocs/op
BenchmarkSignals-16        23143            258628 ns/op            1504 B/op         41 allocs/op
```
