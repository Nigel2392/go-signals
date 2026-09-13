package pubsub2

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/internal/subscriber"
	"github.com/Nigel2392/go-signals/pubsub"
)

type Pool struct {
	*pubsub.BasePool[*Pool]

	// map of topic to signal objects
	signals map[string]PoolSignal

	// map of topic to subscribers,
	subscribers map[string]*subscriber.Subscriber[any]

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(context.Context, *Pool, error)
}

func defaultPoolError(ctx context.Context, p *Pool, err error) {
	log.Printf("error in pool %T: %v", p.MustClient(ctx), err)
}

func New(clientCtx context.Context, pub any, opts ...pubsub.PoolOption) *Pool {
	pool := &Pool{
		BasePool:    pubsub.NewBasePool[*Pool](clientCtx, pub),
		signals:     make(map[string]PoolSignal),
		subscribers: make(map[string]*subscriber.Subscriber[any]),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool)(pool))
	}

	err := pool.BasePool.Initialize(clientCtx)
	if err != nil {
		panic(err)
	}

	if pool.onErr == nil {
		pool.onErr = defaultPoolError
	}

	return pool
}

func GoNew[T any](ctx context.Context, pubsub any, opts ...pubsub.PoolOption) *Pool {
	pool := New(ctx, pubsub, opts...)
	err := pool.BasePool.OnClientInit(ctx, pool.goNew)
	if err != nil {
		panic(err)
	}

	return pool
}

func (p *Pool) goNew(ctx context.Context, _ pubsub.PubSub) error {
	if p.Data == nil {
		go p.Loop(ctx)
		return nil
	}

	go func(pool *Pool) {
		for h, err := range pool.WaitLoop(ctx) {
			if err != nil {
				pool.callErr(ctx, err)
				continue
			}

			if err := h.Process(ctx); err != nil {
				pool.callErr(ctx, err)
			}
		}
	}(p)

	return nil
}

func (p *Pool) TPool[T any]() *TPool[T] {
	return (*TPool[T])(p)
}

func (p *Pool) WithOnError(fn func(context.Context, *Pool, error)) {
	p.onErr = fn
}

func (r *Pool) Close() {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	if r.Exit != nil {
		r.Closed.Store(true)
		close(r.Exit)
		r.Exit = nil
	}
}

func (r *Pool) Get[T any](ctx context.Context, name string) signals.Signal[T] {
	return r.NewSignal[T](ctx, name)
}

func (r *Pool) NewSignal[T any](_ context.Context, name string) signals.Signal[T] {
	r.Mu.Lock()
	sig, ok := r.signals[name]
	defer r.Mu.Unlock()

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
	wg := sync.WaitGroup{}
	wg.Add(1)

	var err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, ps pubsub.PubSub) error {
		wg.Done()
		return nil
	})
	if err != nil {
		return func(yield func(pubsub.Handler[*Pool, any], error) bool) {
			yield(pubsub.Handler[*Pool, any]{}, err)
		}
	}

	wg.Wait()

	if r.Data == nil {
		panic(signals.ErrUnsupported.Wrap(
			"cannot call Pool.Handle without having called Pool.SetChannel",
		))
	}

	return func(yield func(pubsub.Handler[*Pool, any], error) bool) {

		for payload := range r.Data {

			if payload.Error != nil {
				if !yield(pubsub.Handler[*Pool, any]{}, payload.Error) {
					return
				}
				continue
			}

			// retrieve subscriber object and signal
			r.Mu.RLock()
			sub, ok := r.subscribers[payload.Channel]
			if !ok {
				r.Mu.RUnlock()
				continue
			}

			if sub.Pubsub == nil {
				r.Mu.RUnlock()

				panic(fmt.Sprintf(
					"subscriber client is nil but data is being received for channel %s",
					payload.Channel,
				))
			}

			sig, ok := r.signals[payload.Channel]
			if !ok {
				r.Mu.RUnlock()
				continue
			}

			r.Mu.RUnlock()

			// rebuild subscriber cache if required
			// allows for better concurrency
			if sub.Dirty.Load() {
				r.Mu.Lock()
				sub.Undirtify()
				r.Mu.Unlock()
				sub.Dirty.Store(false)
			}

			// see if we should exit the loop
			if r.Closed.Load() {
				return
			}

			if err := ctx.Err(); err != nil {
				yield(pubsub.Handler[*Pool, any]{}, err)
				return
			}

			// decode value to send to receivers
			message, val, err := r.decodeMessage(ctx, sig.MsgType(), payload.Data)
			if err != nil {
				if !yield(pubsub.Handler[*Pool, any]{}, err) {
					return
				}
				continue
			}

			// process
			handler := pubsub.NewHandler[*Pool, any](r.BasePool)
			handler.Value = val
			handler.Signal = sig
			handler.Receivers = sub.Cached
			handler.Message = message

			if !yield(handler, nil) {
				return
			}
		}
	}
}

func (r *Pool) Loop(ctx context.Context) {

	wg := sync.WaitGroup{}
	wg.Add(1)

	var err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, ps pubsub.PubSub) error {
		wg.Done()
		return nil
	})
	if err != nil {
		panic(err)
	}

	wg.Wait()

	if r.Exit != nil {
		panic(signals.ErrUnsupported.Wrap(
			"Pool.Loop() can only be called when in the stopped state",
		))
	}

	if r.Data != nil {
		panic(signals.ErrUnsupported.Wrap(
			"Pool.Loop() cannot be called when synchronous mode is active",
		))
	}

	tick := time.NewTicker(r.TickTime)
	r.Exit = make(chan struct{})

loop:
	for {
		select {
		case <-tick.C:
			if r.doWork(ctx) {
				break loop
			}

		case <-ctx.Done():
			r.callErr(ctx, ctx.Err())
			break loop

		case <-r.Exit:
			break loop
		}
	}

	tick.Stop()
	close(r.Exit)

	r.Mu.Lock()
	r.Exit = nil
	r.Mu.Unlock()
}

func (r *Pool) callErr(ctx context.Context, err error) {
	r.onErr(ctx, (*Pool)(r), err)
}

func (r *Pool) doWork(ctx context.Context) (stop bool) {
	r.Mu.RLock()

	keys := make([]string, 0, len(r.subscribers))
	for k := range r.subscribers {
		keys = append(keys, k)
	}

	r.Mu.RUnlock()

	for _, key := range keys {
		r.Mu.RLock()
		sub, ok := r.subscribers[key]
		if !ok || sub.Pubsub == nil || sub.Receivers == nil || sub.Receivers.Length() == 0 {
			r.Mu.RUnlock()
			continue
		}

		sig, ok := r.signals[key]
		if !ok {
			r.Mu.RUnlock()
			continue
		}

		r.Mu.RUnlock()

		// rebuild subscriber cache if required
		// allows for better concurrency
		if sub.Dirty.Load() {
			r.Mu.Lock()
			sub.Undirtify()
			r.Mu.Unlock()
			sub.Dirty.Store(false)
		}

	drainLoop:
		for {

			// see if we should exit the loop
			if r.Closed.Load() {
				return true
			}

			if err := ctx.Err(); err != nil {
				r.callErr(ctx, err)
				return true
			}

			// try to receive the data
			payload, hasMessage := sub.Pubsub.TryReceive()
			if !hasMessage {
				break drainLoop // Queue empty, move to next subscriber
			}

			msg, val, err := r.decodeMessage(ctx, sig.MsgType(), payload)
			if err != nil {
				r.callErr(ctx, err)
				return true
			}

			newCtx := pubsub.ContextWithMessage(ctx, msg)

			// cast to [pubsub.ProcessorBasePool] to gain access to unexported method
			go (*pubsub.ProcessorBasePool[*Pool])(r.BasePool).
				ProcessReceivers(newCtx, sig, sub.Cached, val, r.callErr)
		}
	}

	return false
}

func (r *Pool) newSub(signal string, createIfNotExists bool) (*subscriber.Subscriber[any], bool) {
	s, ok := r.subscribers[signal]
	if ok {
		return s, false
	}

	if !createIfNotExists {
		return nil, false
	}

	s = &subscriber.Subscriber[any]{
		Receivers: omap.NewOrderedMap(0, signals.Receiver[any].ID),
	}
	r.subscribers[signal] = s
	return s, true
}

func (r *Pool) connect[T any](ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub, isNew := r.newSub(signal, true)
	if isNew {
		err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, c pubsub.PubSub) error {
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

	r.Mu.Lock()
	defer r.Mu.Unlock()

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

	r.Mu.Lock()
	defer r.Mu.Unlock()

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

func (r *Pool) decodeMessage(_ context.Context, typ reflect.Type, data []byte) (msg *pubsub.Message, sentVal any, err error) {
	payload := new(pubsub.Message)
	err = r.Encoder.Decode(bytes.NewReader(data), payload)
	if err != nil {
		return nil, sentVal, err
	}

	val := reflect.New(typ)
	err = r.Encoder.Decode(bytes.NewReader(payload.Data), val.Interface())
	if err != nil {
		return payload, sentVal, err
	}

	return payload, val.Elem().Interface(), err
}
