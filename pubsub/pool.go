package pubsub

import (
	"context"
	"iter"
	"reflect"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
)

var (
	_ PubSubPool[any, *Pool[any]] = (*Pool[any])(nil)
	_ ConfigPool                  = (*Pool[any])(nil)
	_ ConfigErrPool[*Pool[any]]   = (*Pool[any])(nil)
)

type Pool[T any] struct {
	*BasePool[*Pool[T]]

	// map of topic to signal objects
	signals map[string]*signal[T]

	// map of topic to subscribers,
	subscribers map[string]*Sub[T]

	// used to skip a reflect step when decoding messages
	typ reflect.Type
}

func New[T any](clientCtx context.Context, pubsub any, opts ...PoolOption) *Pool[T] {
	pool := &Pool[T]{
		BasePool:    NewBasePool[*Pool[T]](clientCtx, pubsub),
		signals:     make(map[string]*signal[T]),
		subscribers: make(map[string]*Sub[T]),
		typ:         reflect.TypeFor[T](),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool[T])(pool))
	}

	err := pool.BasePool.Initialize(clientCtx)
	if err != nil {
		panic(err)
	}

	return pool
}

func GoNew[T any](ctx context.Context, pubsub any, opts ...PoolOption) *Pool[T] {
	pool := New[T](ctx, pubsub, opts...)
	err := pool.BasePool.P.OnClientInit(ctx, pool.goNew)
	if err != nil {
		panic(err)
	}

	return pool
}

func (p *Pool[T]) goNew(ctx context.Context, _ PubSub) error {
	go func() {
		for err := range GoLoop(ctx, p, 16) {
			p.BasePool.P.OnError(ctx, err)
		}
	}()
	return nil
}

func (r *Pool[T]) Send(ctx context.Context, topic string, value T) error {
	return r.BasePool.Send(ctx, topic, value)
}

func (r *Pool[T]) NewSignal(_ context.Context, name string) signals.Signal[T] {
	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()
	sig, ok := r.signals[name]
	if !ok {
		// slower, but ok.
		// signal creation is meant to be done in the init phase,
		// although it can be done thoughout program lifecycle.
		sig = &signal[T]{name, r}
		r.signals[name] = sig
	}

	return sig
}

// Execute the scheduling loop in a synchronous blocking mode.
//
// if Pool.Data channel is non-nil, synchronous mode is active
//
// synchronous mode does **not** mean that the send/receive
// process is executed in a single goroutine.
//
// synchronous mode is a special mode that likely starts more goroutines (seen in pkg/redis/Subscribe)
// and calling [Pool.Loop] is deemed illegal and causes a panic.
//
// On the upside, it does not rely on the ticker to retrieve values.
// this means that any values sent from a signal propagate as
// quickly as possible, only being limited by the scheduler.
//
// Using this function is also great for benchmarking, as it isnt reliant on the timer.
func (r *Pool[T]) WaitLoop(ctx context.Context) iter.Seq2[Handler[*Pool[T], T], error] {
	return r.P.WaitLoop(ctx, r.subscribers, r.signals)
}

func (r *Pool[T]) Cycle(ctx context.Context, resend bool) (Processor, error) {
	return r.P.Cycle(ctx, r.subscribers, r.signals, resend)
}

func (r *Pool[T]) newSub(signal string, createIfNotExists bool) (*Sub[T], bool) {
	s, ok := r.subscribers[signal]
	if ok {
		return s, false
	}

	if !createIfNotExists {
		return nil, false
	}

	s = &Sub[T]{
		Receivers: omap.NewOrderedMap(0, signals.Receiver[T].ID),
	}
	r.subscribers[signal] = s
	return s, true
}

func (r *Pool[T]) connect(ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()

	sub, isNew := r.newSub(signal, true)
	if isNew {
		err = r.BasePool.P.OnClientInit(ctx, func(ctx context.Context, c PubSub) error {
			sub.Pubsub, err = c.Subscribe(ctx, signal)
			return err
		})
	}

	sub.Add(recv)

	return err
}

func (r *Pool[T]) clear(ctx context.Context, signal string) error {
	if ContextIs(ctx, "disconnect", signal, r) {
		return nil
	}

	ctx = ContextWith(ctx, "disconnect", signal, r)

	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()

	sub, _ := r.newSub(signal, false)
	if sub == nil || sub.Receivers == nil || sub.Receivers.Length() == 0 {
		return nil
	}

	for idx, recv := range sub.Receivers.List() {
		id := recv.ID()
		err := recv.Disconnect(ctx)
		if err != nil {
			return signals.ErrReceiver.WithCause(err).Wrapf(
				"[%d] receiver %q", idx, id,
			)
		}
	}

	sub.Clear()

	return sub.Check(signal)
}

func (r *Pool[T]) disconnect(ctx context.Context, sig *signal[T], recv signals.Receiver[T]) error {
	if ContextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = ContextWith(ctx, "disconnect", sig.name, r)

	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()

	sub, _ := r.newSub(sig.name, false)
	if sub == nil || sub.Receivers == nil || sub.Receivers.Length() == 0 {
		return nil
	}

	didDel := sub.Del(recv)
	if didDel {
		err := recv.Disconnect(ctx)
		if err != nil {
			return err
		}
	}

	return sub.Check(sig.name)
}
