package pubsub2

import (
	"context"
	"fmt"
	"iter"
	"reflect"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/pubsub"
)

var _ pubsub.AbstractPool = (*Pool)(nil)

type Pool struct {
	*pubsub.BasePool[*Pool]

	// map of topic to signal objects
	signals map[string]pubsub.PoolSignal[any]

	// map of topic to subscribers,
	subscribers map[string]*pubsub.Sub[any]
}

func New(clientCtx context.Context, pub any, opts ...pubsub.PoolOption) *Pool {
	pool := &Pool{
		BasePool:    pubsub.NewBasePool[*Pool](clientCtx, pub),
		signals:     make(map[string]pubsub.PoolSignal[any]),
		subscribers: make(map[string]*pubsub.Sub[any]),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool)(pool))
	}

	err := pool.BasePool.Initialize(clientCtx)
	if err != nil {
		panic(err)
	}

	return pool
}

func GoNew(ctx context.Context, pubsub any, opts ...pubsub.PoolOption) *Pool {
	pool := New(ctx, pubsub, opts...)
	err := pool.BasePool.P.OnClientInit(ctx, pool.goNew)
	if err != nil {
		panic(err)
	}

	return pool
}

func (p *Pool) goNew(ctx context.Context, _ pubsub.PubSub) error {
	go func() {
		for err := range pubsub.GoLoop(ctx, p, 16) {
			p.BasePool.P.OnError(ctx, err)
		}
	}()
	return nil
}

func (p *Pool) TPool[T any]() *TPool[T] {
	return (*TPool[T])(p)
}

func (r *Pool) Get[T any](ctx context.Context, name string) signals.Signal[T] {
	return r.NewSignal[T](ctx, name)
}

func (r *Pool) NewSignal[T any](_ context.Context, name string) signals.Signal[T] {
	r.P.Mu.Lock()
	sig, ok := r.signals[name]
	defer r.P.Mu.Unlock()

	if !ok {
		// slower, but ok.
		// signal creation is meant to be done in the init phase,
		// although it can be done thoughout program lifecycle.
		sig = &wrappedSignal[T]{
			name: name,
			pool: r,
		}
		r.signals[name] = sig
	}

	if chkTyp := reflect.TypeFor[T](); chkTyp != sig.MsgType() {
		panic(fmt.Sprintf("%s does not match required type %s", chkTyp, sig.MsgType()))
	}

	return (*signal[T])(sig.(*wrappedSignal[T]))
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
func (r *Pool) WaitLoop(ctx context.Context) iter.Seq2[pubsub.Handler[*Pool, any], error] {
	return r.P.WaitLoop(ctx, r.subscribers, r.signals)
}

func (r *Pool) Cycle(ctx context.Context, resend bool) (pubsub.Processor, error) {
	return r.P.Cycle(ctx, r.subscribers, r.signals, resend)
}

func (r *Pool) newSub(signal string, createIfNotExists bool) (*pubsub.Sub[any], bool) {
	s, ok := r.subscribers[signal]
	if ok {
		return s, false
	}

	if !createIfNotExists {
		return nil, false
	}

	s = &pubsub.Sub[any]{
		Receivers: omap.NewOrderedMap(0, signals.Receiver[any].ID),
	}
	r.subscribers[signal] = s
	return s, true
}

func (r *Pool) connect[T any](ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()

	sub, isNew := r.newSub(signal, true)
	if isNew {
		err = r.BasePool.P.OnClientInit(ctx, func(ctx context.Context, c pubsub.PubSub) error {
			sub.Pubsub, err = c.Subscribe(ctx, signal)
			return err
		})
	}

	// immediately return
	// subscriber was already initialized
	if re, ok := recv.(*receiver[T]); ok {
		sub.Add((*wrappedReceiver[T])(re))
	} else {
		sub.Add(new(ifaceReceiver[T]{recv}))
	}

	return err
}

func (r *Pool) clear(ctx context.Context, signal string) error {
	if pubsub.ContextIs(ctx, "disconnect", signal, r) {
		return nil
	}

	ctx = pubsub.ContextWith(ctx, "disconnect", signal, r)

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

func (r *Pool) disconnect[T any](ctx context.Context, sig *signal[T], recv *receiver[T]) error {
	if pubsub.ContextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = pubsub.ContextWith(ctx, "disconnect", sig.name, r)

	r.P.Mu.Lock()
	defer r.P.Mu.Unlock()

	sub, _ := r.newSub(sig.name, false)
	if sub == nil || sub.Receivers == nil || sub.Receivers.Length() == 0 {
		return nil
	}

	didDel := sub.Del((*wrappedReceiver[T])(recv))
	if didDel {
		err := recv.Disconnect(ctx)
		if err != nil {
			return err
		}
	}

	return sub.Check(sig.name)
}
