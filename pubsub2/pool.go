package pubsub2

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"log"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type RWLocker interface {
	sync.Locker
	RLock()
	RUnlock()
}

type Pool struct {
	mu RWLocker

	// inst provides the instance ID for this pool object.
	//
	// if none is provided through the options,
	// this is automatically generated with uuid.NewUUID.
	inst uuid.UUID

	// function to encode any values published.
	//
	// the default encoder is JSON.
	encoder encoder.Encoder

	// the underlying interface that handles
	// data transmission and retrieval
	client pubsub.PubSub

	// map of topic to signal objects
	signals map[string]PoolSignal

	// map of topic to subscribers,
	subscribers map[string]*subscriber

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(*Pool, error)

	// channel for running in synchronous mode with WaitLoop
	//
	// if channel is non-nil, synchronous mode is active
	//
	// synchronous mode does **not** mean that the send/receive
	// process is executed in a single goroutine.
	//
	// synchronous mode is a special mode that likely starts more goroutines (seen in pkg/redis/Subscribe)
	// and calling [Pool.Loop] is deemed illegal, and causes a panic.
	//
	// on the upside, it does not rely on the ticker to retrieve values.
	// this means that any values sent from a signal propagate as
	// quickly as possible, only being limited by the scheduler.
	data chan pubsub.Message

	// managing the loop
	tickTime time.Duration // only used when in async/loop mode

	// fast-path to check if currently in non-running state
	// selecting on `exit` makes the `Pool` slower by *orders of magnitude.*
	closed atomic.Bool
	exit   chan struct{}
}

func defaultPoolError(p *Pool, err error) {
	log.Printf("error in pool %T: %v", p.client, err)
}

func New(pub pubsub.PubSub, opts ...PoolOption) *Pool {
	pool := &Pool{
		mu:          &sync.RWMutex{},
		client:      pub,
		signals:     make(map[string]PoolSignal),
		subscribers: make(map[string]*subscriber),
	}

	for _, opt := range opts {
		opt((*Pool)(pool))
	}

	if pool.encoder == nil {
		pool.encoder = encoder.NewJSONEncoder()
	}

	if pool.tickTime == 0 {
		pool.tickTime = time.Millisecond / 2
	}

	if pool.onErr == nil {
		pool.onErr = defaultPoolError
	}

	if (pool.inst == uuid.UUID{}) {
		pool.inst = uuid.New()
	}

	if b, ok := pub.(pubsub.PubSubBinder); ok {
		b.BindChannel(pool)
	}

	return pool
}

func GoNew(ctx context.Context, pubsub pubsub.PubSub, opts ...PoolOption) *Pool {
	var pool = New(pubsub, opts...)
	if pool.data == nil {
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

func (r *Pool) ID() uuid.UUID {
	return r.inst
}

func (r *Pool) Client() pubsub.PubSub {
	return r.client
}

func (r *Pool) SetChannel(ch chan pubsub.Message) {
	r.data = ch
}

func (r *Pool) Channel() chan pubsub.Message {
	return r.data
}

func (r *Pool) Send[T any](ctx context.Context, topic string, value T) error {
	message, err := r.newMessage(ctx, topic, value)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T with %T", value, r.encoder,
		)
	}

	data, err := r.encoder.EncodeBytes(message)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T with %T", value, r.encoder,
		)
	}

	err = r.client.Publish(ctx, topic, data)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not publish %T", value,
		)
	}

	return nil
}

func (r *Pool) Close() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.exit != nil {
		r.closed.Store(true)
		close(r.exit)
		r.exit = nil
	}
}

func (r *Pool) NewSignal[T any](_ context.Context, name string) signals.Signal[T] {
	r.mu.Lock()
	sig, ok := r.signals[name]
	defer r.mu.Unlock()

	if !ok {
		// slower, but ok.
		// signal creation is meant to be done in the init phase,
		// although it can be done thoughout program lifecycle.
		sig = &wrappedSignal[T]{
			typ:  reflect.TypeFor[T](),
			name: name,
			pool: r,
		}
		r.signals[name] = sig
	}

	typedSig := (*signal[T])(sig.(*wrappedSignal[T]))
	if chkTyp := reflect.TypeFor[T](); chkTyp != typedSig.typ {
		panic(fmt.Sprintf("%s does not match required type %s", chkTyp, typedSig.typ))
	}

	return typedSig
}

// Execute the scheduling loop in a synchronous blocking mode.
//
// if Pool.data channel is non-nil, synchronous mode is active
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
func (r *Pool) WaitLoop(ctx context.Context) iter.Seq2[*Handler, error] {
	if r.data == nil {
		panic(signals.ErrUnsupported.Wrap(
			"cannot call Pool.Handle without having called Pool.SetChannel",
		))
	}

	return func(yield func(*Handler, error) bool) {
		for payload := range r.data {

			// retrieve subscriber object and signal
			r.mu.RLock()
			sub, ok := r.subscribers[payload.Channel]
			if !ok {
				r.mu.RUnlock()
				continue
			}

			sig, ok := r.signals[payload.Channel]
			if !ok {
				r.mu.RUnlock()
				continue
			}

			r.mu.RUnlock()

			// rebuild subscriber cache if required
			// allows for better concurrency
			if sub._dirty.Load() {
				r.mu.Lock()
				sub._undirtify()
				r.mu.Unlock()
				sub._dirty.Store(false)
			}

			// see if we should exit the loop
			if r.closed.Load() {
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
			var handler = &Handler{
				pool:      r,
				Value:     val,
				Signal:    sig,
				Receivers: sub._cached,
				Message:   message,
			}

			if !yield(handler, nil) {
				return
			}
		}
	}
}

func (r *Pool) Loop(ctx context.Context) {
	if r.exit != nil {
		panic(signals.ErrUnsupported.Wrap(
			"Pool.Loop() can only be called when in the stopped state",
		))
	}

	if r.data != nil {
		panic(signals.ErrUnsupported.Wrap(
			"Pool.Loop() cannot be called when synchronous mode is active",
		))
	}

	tick := time.NewTicker(r.tickTime)
	r.exit = make(chan struct{})

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

		case <-r.exit:
			break loop
		}
	}

	tick.Stop()
	close(r.exit)

	r.mu.Lock()
	r.exit = nil
	r.mu.Unlock()
}

func (r *Pool) callErr(err error) {
	r.onErr((*Pool)(r), err)
}

func (r *Pool) doWork(ctx context.Context) (stop bool) {
	r.mu.RLock()

	keys := make([]string, 0, len(r.subscribers))
	for k := range r.subscribers {
		keys = append(keys, k)
	}

	r.mu.RUnlock()

	for _, key := range keys {
		r.mu.RLock()
		sub, ok := r.subscribers[key]
		if !ok {
			r.mu.RUnlock()
			continue
		}

		if sub.receivers == nil || sub.receivers.length() == 0 {
			continue
		}

		sig, ok := r.signals[key]
		if !ok {
			r.mu.RUnlock()
			continue
		}

		r.mu.RUnlock()

		// rebuild subscriber cache if required
		// allows for better concurrency
		if sub._dirty.Load() {
			r.mu.Lock()
			sub._undirtify()
			r.mu.Unlock()
			sub._dirty.Store(false)
		}

	drainLoop:
		for {

			// see if we should exit the loop
			if r.closed.Load() {
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

			newCtx := contextWithMessage(ctx, msg)
			go r.processReceivers(newCtx, sig, sub._cached, val, r.callErr)
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
		receivers: newOrderedMap[any](0),
	}
	r.subscribers[signal] = s
	return s
}

func (r *Pool) connect[T any](ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, true)

	newPubSub := sub.pubsub == nil
	if newPubSub {
		sub.pubsub, err = r.client.Subscribe(ctx, signal)
	}

	if re, ok := recv.(*receiver[T]); ok {
		sub.add((*wrappedReceiver[T])(re))
	} else {
		sub.add(&ifaceReceiver[T]{recv})
	}

	return err
}

func (r *Pool) clear(ctx context.Context, signal string) error {
	if contextIs(ctx, "disconnect", signal, r) {
		return nil
	}

	ctx = contextWith(ctx, "disconnect", signal, r)

	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, false)
	if sub == nil || sub.receivers == nil || sub.receivers.length() == 0 {
		return nil
	}

	for idx, recv := range sub.receivers.list() {
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
	if contextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = contextWith(ctx, "disconnect", sig.name, r)

	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(sig.name, false)
	if sub == nil || sub.receivers == nil || sub.receivers.length() == 0 {
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
	err = r.encoder.Decode(bytes.NewReader(data), payload)
	if err != nil {
		return nil, sentVal, err
	}

	val := reflect.New(typ)
	err = r.encoder.Decode(bytes.NewReader(payload.Data), val.Interface())
	if err != nil {
		return payload, sentVal, err
	}

	return payload, val.Elem().Interface(), err
}

func (r *Pool) newMessage[T any](ctx context.Context, topic string, value T) (message *pubsub.Message, err error) {
	var data []byte
	if any(value) != nil {
		data, err = r.encoder.EncodeBytes(value)
		if err != nil {
			return nil, err
		}
	}

	message = new(pubsub.Message{
		Channel: topic,
		Sender:  r.inst,
		Data:    data,
		Meta:    msgMetaFromContext(ctx, r),
	})

	if message.Meta == nil {
		message.Meta = make(map[string]any)
	}

	if maker, ok := r.client.(pubsub.PubSubMsgMaker); ok {
		message = maker.MakeMessage(ctx, topic, message, true)
	}

	return message, nil
}

type contextKey struct {
	name    string
	topic   string
	pointer uintptr
}

func newKey[T any](usage string, topic string, onceObj *T) contextKey {
	var ptr uintptr
	if onceObj != nil {
		ptr = uintptr(unsafe.Pointer(onceObj))
	}

	return contextKey{
		name:    usage,
		topic:   topic,
		pointer: ptr,
	}
}

func contextWith[T any](ctx context.Context, usage string, topic string, onceObj *T) context.Context {
	return context.WithValue(ctx, newKey(usage, topic, onceObj), struct{}{})
}

func contextIs[T any](ctx context.Context, usage string, topic string, onceObj *T) bool {
	_, ok := ctx.Value(newKey(usage, topic, onceObj)).(struct{})
	return ok
}

var (
	poolContextKey        = newKey[struct{}]("pubsub.PoolFromContext", "", nil)
	messageMetaContextKey = newKey[struct{}]("pubsub.msgMetaFromContext", "", nil)
)

func PoolFromContext(ctx context.Context) *Pool {
	var v, _ = ctx.Value(poolContextKey).(*Pool)
	return v
}

//go:linkname contextWithMessage github.com/Nigel2392/go-signals/pubsub.contextWithMessage
func contextWithMessage(ctx context.Context, msg *pubsub.Message) context.Context

func contextWithPool(ctx context.Context, pool *Pool) context.Context {
	return context.WithValue(ctx, poolContextKey, pool)
}

func msgMetaFromContext(ctx context.Context, pool *Pool) (meta map[string]any) {
	var v, _ = ctx.Value(messageMetaContextKey).(func(context.Context, *Pool) map[string]any)
	if v != nil {
		meta = v(ctx, pool)
	}
	return meta
}

//	//go:nosplit
//	func noescape(p unsafe.Pointer) unsafe.Pointer {
//		x := uintptr(p)
//		return unsafe.Pointer(x ^ 0)
//	}
