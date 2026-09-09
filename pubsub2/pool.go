package pubsub2

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"log"
	"reflect"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/pubsub"
)

type Pool struct {
	pubsub.BasePool

	// map of topic to signal objects
	signals map[string]PoolSignal

	// map of topic to subscribers,
	subscribers map[string]*subscriber

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(*Pool, error)
}

func defaultPoolError(p *Pool, err error) {
	log.Printf("error in pool %T: %v", p.Client(), err)
}

func New(pub pubsub.PubSub, opts ...pubsub.PoolOption) *Pool {
	pool := &Pool{
		BasePool:    pubsub.NewBasePool(pub),
		signals:     make(map[string]PoolSignal),
		subscribers: make(map[string]*subscriber),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool)(pool))
	}

	pool.BasePool.Setup()

	if pool.onErr == nil {
		pool.onErr = defaultPoolError
	}

	return pool
}

func GoNew(ctx context.Context, pubsub pubsub.PubSub, opts ...pubsub.PoolOption) *Pool {
	var pool = New(pubsub, opts...)
	if pool.Data == nil {
		go pool.Loop(ctx)
		return pool
	}
	go func() {
		for h, err := range pool.WaitLoop(ctx) {
			if err != nil {
				pool.callErr(err)
				continue
			}

			if err := h.Process(ctx); err != nil {
				pool.callErr(err)
			}
		}
	}()
	return pool
}

func (p *Pool) WithOnError(fn func(*Pool, error)) {
	p.onErr = fn
}

func (r *Pool) Send[T any](ctx context.Context, topic string, value T) error {
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

	err = r.Client().Publish(ctx, topic, data)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not publish %T", value,
		)
	}

	return nil
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

	typedSig := (*signal[T])(sig.(*wrappedSignal[T]))
	if chkTyp := reflect.TypeFor[T](); chkTyp != typedSig.MsgType() {
		panic(fmt.Sprintf("%s does not match required type %s", chkTyp, typedSig.typ))
	}

	return typedSig
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
func (r *Pool) WaitLoop(ctx context.Context) iter.Seq2[*pubsub.Handler[any], error] {
	if r.Data == nil {
		panic(signals.ErrUnsupported.Wrap(
			"cannot call Pool.Handle without having called Pool.SetChannel",
		))
	}

	return func(yield func(*pubsub.Handler[any], error) bool) {
		for payload := range r.Data {

			// retrieve subscriber object and signal
			r.Mu.RLock()
			sub, ok := r.subscribers[payload.Channel]
			if !ok {
				r.Mu.RUnlock()
				continue
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
				yield(nil, err)
				return
			}

			// decode value to send to receivers
			message, val, err := r.decodeMessage(ctx, sig.MsgType(), payload.Data)
			if err != nil {
				yield(nil, err)
				return
			}

			// process
			handler := pubsub.NewHandler[any](&r.BasePool)
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

func (r *Pool) Loop(ctx context.Context) {
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
			r.callErr(ctx.Err())
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

func (r *Pool) callErr(err error) {
	r.onErr((*Pool)(r), err)
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
		if !ok {
			r.Mu.RUnlock()
			continue
		}

		if sub.receivers == nil || sub.receivers.Length() == 0 {
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
				r.callErr(err)
				return true
			}

			// try to receive the data
			payload, hasMessage := sub.pubsub.TryReceive()
			if !hasMessage {
				break drainLoop // Queue empty, move to next subscriber
			}

			msg, val, err := r.decodeMessage(ctx, sig.MsgType(), payload)
			if err != nil {
				r.callErr(err)
				return true
			}

			newCtx := pubsub.ContextWithMessage(ctx, msg)

			// cast to [pubsub.ProcessorBasePool] to gain access to unexported method
			go (*pubsub.ProcessorBasePool)(&r.BasePool).
				ProcessReceivers(newCtx, sig, sub._cached, val, r.callErr)
		}
	}

	return false
}

func (r *Pool) newSub(signal string, createIfNotExists bool) *subscriber {
	s, ok := r.subscribers[signal]
	if ok {
		return s
	}

	if !createIfNotExists {
		return nil
	}

	s = &subscriber{
		receivers: omap.NewOrderedMap[any](0),
	}
	r.subscribers[signal] = s
	return s
}

func (r *Pool) connect[T any](ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub := r.newSub(signal, true)

	newPubSub := sub.pubsub == nil
	if newPubSub {
		sub.pubsub, err = r.Client().Subscribe(ctx, signal)
	}

	if re, ok := recv.(*receiver[T]); ok {
		sub.add((*wrappedReceiver[T])(re))
	} else {
		sub.add(new(ifaceReceiver[T]{recv}))
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

func (r *Pool) disconnect[T any](ctx context.Context, sig *signal[T], recv *receiver[T]) error {
	if pubsub.ContextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = pubsub.ContextWith(ctx, "disconnect", sig.name, r)

	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub := r.newSub(sig.name, false)
	if sub == nil || sub.receivers == nil || sub.receivers.Length() == 0 {
		return nil
	}

	didDel := sub.del((*wrappedReceiver[T])(recv))
	if didDel {
		err := recv.Disconnect(ctx)
		if err != nil {
			return err
		}
	}

	return sub.check(sig.name)
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

func (r *Pool) newMessage[T any](ctx context.Context, topic string, value T) (message *pubsub.Message, err error) {
	var data []byte
	if any(value) != nil {
		data, err = r.Encoder.EncodeBytes(value)
		if err != nil {
			return nil, err
		}
	}

	message = new(pubsub.Message{
		Channel: topic,
		Sender:  r.Inst,
		Data:    data,
		Meta:    pubsub.MsgMetaFromContext(ctx, r),
	})

	if message.Meta == nil {
		message.Meta = make(map[string]any)
	}

	if maker, ok := r.Client().(pubsub.PubSubMsgMaker); ok {
		message = maker.MakeMessage(ctx, topic, message, true)
	}

	return message, nil
}
