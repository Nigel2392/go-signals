package pubsub

import (
	"context"
	"fmt"
	"iter"
	"log"
	"sync"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
)

var (
	_ PubSubPool[any]           = (*Pool[any])(nil)
	_ ConfigPool                = (*Pool[any])(nil)
	_ ConfigErrPool[*Pool[any]] = (*Pool[any])(nil)
)

type Pool[T any] struct {
	*BasePool

	// map of topic to signal objects
	signals map[string]*signal[T]

	// map of topic to subscribers,
	subscribers map[string]*subscriber[T]

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(context.Context, *Pool[T], error)
}

func defaultPoolError[T any](ctx context.Context, p *Pool[T], err error) {
	log.Printf("error in pool %T: %v", p.MustClient(ctx), err)
}

func New[T any](pubsub any, opts ...PoolOption) *Pool[T] {
	pool := &Pool[T]{
		BasePool:    NewBasePool(pubsub),
		signals:     make(map[string]*signal[T]),
		subscribers: make(map[string]*subscriber[T]),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool[T])(pool))
	}

	pool.BasePool.Initialize()

	if pool.onErr == nil {
		pool.onErr = defaultPoolError
	}

	return pool
}

func GoNew[T any](ctx context.Context, pubsub any, opts ...PoolOption) *Pool[T] {
	pool := New[T](pubsub, opts...)
	err := pool.BasePool.OnClientInit(ctx, pool.goNew)
	if err != nil {
		panic(err)
	}

	return pool
}

func (p *Pool[T]) goNew(ctx context.Context, _ PubSub) error {
	if p.Data == nil {
		go p.Loop(ctx)
		return nil
	}

	go func(pool *Pool[T]) {
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

func (p *Pool[T]) WithOnError(fn func(context.Context, *Pool[T], error)) {
	p.onErr = fn
}

func (r *Pool[T]) Send(ctx context.Context, topic string, value T) error {
	message, err := r.newMessage(ctx, topic, value)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T with %T", value, r.Encoder,
		)
	}

	data, err := r.Encoder.EncodeBytes(message)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T with %T", value, r.Encoder,
		)
	}

	c, err := r.Client(ctx)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not initialize client %T", c,
		)
	}

	err = c.Publish(ctx, topic, data)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not publish %T", value,
		)
	}

	return nil
}

func (r *Pool[T]) Close() {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	if r.Exit != nil {
		r.Closed.Store(true)
		close(r.Exit)
		r.Exit = nil
	}
}

func (r *Pool[T]) NewSignal(_ context.Context, name string) signals.Signal[T] {
	r.Mu.RLock()
	sig, ok := r.signals[name]
	r.Mu.RUnlock()

	if !ok {
		// slower, but ok.
		// signal creation is meant to be done in the init phase,
		// although it can be done thoughout program lifecycle.
		r.Mu.Lock()
		sig = &signal[T]{name, r}
		r.signals[name] = sig
		r.Mu.Unlock()
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
func (r *Pool[T]) WaitLoop(ctx context.Context) iter.Seq2[Handler[T], error] {

	wg := sync.WaitGroup{}
	wg.Add(1)

	var err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, ps PubSub) error {
		wg.Done()
		return nil
	})
	if err != nil {
		return func(yield func(Handler[T], error) bool) {
			yield(Handler[T]{}, err)
		}
	}

	wg.Wait()

	if r.Data == nil {
		panic(signals.ErrUnsupported.Wrap(
			"cannot call Pool.Handle without having called Pool.SetChannel",
		))
	}

	return func(yield func(Handler[T], error) bool) {

		for payload := range r.Data {

			// retrieve subscriber object and signal
			r.Mu.RLock()
			sub, ok := r.subscribers[payload.Channel]
			if !ok {
				r.Mu.RUnlock()
				continue
			}

			if sub.pubsub == nil {
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
			if sub._dirty.Load() {
				r.Mu.Lock()
				sub._undirtify()
				r.Mu.Unlock()
				sub._dirty.Store(false)
			}

			// see if we should exit the loop
			if r.Closed.Load() {
				return
			}

			if err := ctx.Err(); err != nil {
				yield(Handler[T]{}, err)
				return
			}

			// decode value to send to receivers
			message, val, err := r.decodeMessage[T](ctx, payload.Data)
			if err != nil {
				yield(Handler[T]{}, err)
				return
			}

			// process
			handler := NewHandler[T](r.BasePool)
			handler.Value = val
			handler.Signal = sig
			handler.Receivers = sub._cached
			handler.Message = message
			if !yield(handler, nil) {
				return
			}
		}
	}
}

func (r *Pool[T]) Loop(ctx context.Context) {

	if !r.BasePool.ClientWasSetup() {
		err := r.BasePool.OnClientInit(ctx, func(ctx context.Context, ps PubSub) error {
			go r.Loop(ctx)
			return nil
		})
		if err != nil {
			panic(signals.ErrSignal.WithCause(err).Wrap(
				"error during initialization of client",
			))
		}
		return
	}

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

func (r *Pool[T]) callErr(ctx context.Context, err error) {
	r.onErr(ctx, (*Pool[T])(r), err)
}

func (r *Pool[T]) doWork(ctx context.Context) (stop bool) {
	r.Mu.RLock()

	keys := make([]string, 0, len(r.subscribers))
	for k := range r.subscribers {
		keys = append(keys, k)
	}

	r.Mu.RUnlock()

	for _, key := range keys {
		r.Mu.RLock()
		sub, ok := r.subscribers[key]
		if !ok || sub.pubsub == nil || sub.receivers == nil || sub.receivers.Length() == 0 {
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
		if sub._dirty.Load() {
			r.Mu.Lock()
			sub._undirtify()
			r.Mu.Unlock()
			sub._dirty.Store(false)
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
			payload, hasMessage := sub.pubsub.TryReceive()
			if !hasMessage {
				break drainLoop // Queue empty, move to next subscriber
			}

			msg, val, err := r.decodeMessage[T](ctx, payload)
			if err != nil {
				r.callErr(ctx, err)
				return true
			}

			newCtx := ContextWithMessage(ctx, msg)
			go r.processReceivers(newCtx, sig, sub._cached, val, r.callErr)
		}
	}

	return false
}

func (r *Pool[T]) newSub(signal string, createIfNotExists bool) *subscriber[T] {
	s, ok := r.subscribers[signal]
	if ok {
		return s
	}

	if !createIfNotExists {
		return nil
	}

	s = &subscriber[T]{
		receivers: omap.NewOrderedMap(0, signals.Receiver[T].ID),
	}
	r.subscribers[signal] = s
	return s
}

func (r *Pool[T]) connect(ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub := r.newSub(signal, true)

	newPubSub := sub.pubsub == nil
	if !newPubSub {
		// immediately return
		// subscriber was already initialized
		sub.add(recv)
		return nil
	}

	if r.BasePool.ClientWasSetup() {
		var c PubSub
		c, err = r.Client(ctx)
		if err != nil {
			return err
		}

		sub.pubsub, err = c.Subscribe(ctx, signal)
	} else {
		err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, c PubSub) error {
			sub.pubsub, err = c.Subscribe(ctx, signal)
			return err
		})
	}

	sub.add(recv)

	return err
}

func (r *Pool[T]) clear(ctx context.Context, signal string) error {
	if ContextIs(ctx, "disconnect", signal, r) {
		return nil
	}

	ctx = ContextWith(ctx, "disconnect", signal, r)

	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub := r.newSub(signal, false)
	if sub == nil || sub.receivers == nil || sub.receivers.Length() == 0 {
		return nil
	}

	for idx, recv := range sub.receivers.List() {
		id := recv.ID()
		err := recv.Disconnect(ctx)
		if err != nil {
			return signals.ErrReceiver.WithCause(err).Wrapf(
				"[%d] receiver %q", idx, id,
			)
		}
	}

	sub.clear()

	return sub.check(signal)
}

func (r *Pool[T]) disconnect(ctx context.Context, sig *signal[T], recv signals.Receiver[T]) error {
	if ContextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = ContextWith(ctx, "disconnect", sig.name, r)

	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub := r.newSub(sig.name, false)
	if sub == nil || sub.receivers == nil || sub.receivers.Length() == 0 {
		return nil
	}

	didDel := sub.del(recv)
	if didDel {
		err := recv.Disconnect(ctx)
		if err != nil {
			return err
		}
	}

	return sub.check(sig.name)
}

func (r *Pool[T]) newMessage(ctx context.Context, topic string, value T) (message *Message, err error) {
	var data []byte
	if any(value) != nil {
		data, err = r.Encoder.EncodeBytes(value)
		if err != nil {
			return nil, err
		}
	}

	message = new(Message{
		Channel: topic,
		Sender:  r.Inst,
		Data:    data,
		Meta:    MsgMetaFromContext(ctx, r),
	})

	if message.Meta == nil {
		message.Meta = make(map[string]any)
	}

	c, err := r.Client(ctx)
	if err != nil {
		return message, err
	}

	if maker, ok := c.(PubSubMsgMaker); ok {
		message = maker.MakeMessage(ctx, topic, message, true)
	}

	return message, nil
}
