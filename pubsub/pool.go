package pubsub

import (
	"bytes"
	"context"
	"iter"
	"log"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
	"github.com/elliotchance/orderedmap/v2"
)

var _ PubSubPool[any] = (*Pool[any])(nil)

type Pool[T any] struct {
	mu sync.RWMutex

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
	client PubSub

	// map of topic to signal objects
	signals map[string]*signal[T]

	// map of topic to subscribers,
	subscribers map[string]*subscriber[T]

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(*Pool[T], error)

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
	data chan *Message

	// managing the loop
	tickTime time.Duration // only used when in async/loop mode

	// fast-path to check if currently in non-running state
	// selecting on `exit` makes the `Pool` slower by *orders of magnitude.*
	closed atomic.Bool
	exit   chan struct{}
}

func defaultPoolError[T any](p *Pool[T], err error) {
	log.Printf("error in pool %T: %v", p.client, err)
}

func New[T any](pubsub PubSub, opts ...PoolOption[T]) *Pool[T] {
	pool := &Pool[T]{
		mu:          sync.RWMutex{},
		client:      pubsub,
		signals:     make(map[string]*signal[T]),
		subscribers: make(map[string]*subscriber[T]),
	}

	for _, opt := range opts {
		opt((*Pool[T])(pool))
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

	if b, ok := pubsub.(PubSubBinder); ok {
		b.BindChannel(pool)
	}

	return pool
}

func GoNew[T any](ctx context.Context, pubsub PubSub, opts ...PoolOption[T]) *Pool[T] {
	var pool = New[T](pubsub, opts...)
	if pool.data == nil {
		go pool.Loop(ctx)
		return pool
	}
	go func() {
		for range pool.WaitLoop(ctx) {
		}
	}()
	return pool
}

func (r *Pool[T]) ID() uuid.UUID {
	return r.inst
}

func (r *Pool[T]) Client() PubSub {
	return r.client
}

func (r *Pool[T]) SetChannel(ch chan *Message) {
	r.data = ch
}

func (r *Pool[T]) Channel() chan *Message {
	return r.data
}

func (r *Pool[T]) Send(ctx context.Context, topic string, value T) error {
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

func (r *Pool[T]) Close() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.exit != nil {
		r.closed.Store(true)
		close(r.exit)
		r.exit = nil
	}
}

func (r *Pool[T]) NewSignal(_ context.Context, name string) signals.Signal[T] {
	r.mu.RLock()
	sig, ok := r.signals[name]
	r.mu.RUnlock()

	if !ok {
		// slower, but ok.
		// signal creation is meant to be done in the init phase,
		// although it can be done thoughout program lifecycle.
		r.mu.Lock()
		sig = &signal[T]{name, r}
		r.signals[name] = sig
		r.mu.Unlock()
	}

	return sig
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
func (r *Pool[T]) WaitLoop(ctx context.Context) iter.Seq2[*Handler[T], error] {
	if r.data == nil {
		panic(signals.ErrUnsupported.Wrap(
			"cannot call Pool.Handle without having called Pool.SetChannel",
		))
	}

	return func(yield func(*Handler[T], error) bool) {
		for payload := range r.data {

			// retrieve subscriber object and signal
			r.mu.Lock()
			sub, ok := r.subscribers[payload.Channel]
			if !ok {
				r.mu.Unlock()
				continue
			}

			sig, ok := r.signals[payload.Channel]
			if !ok {
				r.mu.Unlock()
				continue
			}

			// rebuild subscriber cache if required
			// allows for better concurrency
			sub.checkDirty()
			r.mu.Unlock()

			// see if we should exit the loop
			if r.closed.Load() {
				return
			}

			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}

			// decode value to send to receivers
			message, val, err := r.decodeMessage(ctx, payload.Data)
			if err != nil {
				yield(nil, err)
				return
			}

			// process
			var handler = &Handler[T]{
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

func (r *Pool[T]) Loop(ctx context.Context) {
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

func (r *Pool[T]) callErr(err error) {
	r.onErr((*Pool[T])(r), err)
}

func (r *Pool[T]) doWork(ctx context.Context) (stop bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for pubName, sub := range r.subscribers {

		if sub.receivers == nil || sub.receivers.Len() == 0 {
			continue
		}

		sig, ok := r.signals[pubName]
		if !ok {
			continue
		}

		sub.checkDirty()

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

			msg, val, err := r.decodeMessage(ctx, payload)
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

func (r *Pool[T]) processReceivers(ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(error)) {

	ctx = contextWithPool(ctx, r)

receiverLoop:
	for _, receiver := range receivers {

		if r.closed.Load() || ctx.Err() != nil {
			return
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			callErr(signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
			continue receiverLoop
		}
	}
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
		receivers: orderedmap.NewOrderedMap[string, signals.Receiver[T]](),
	}
	r.subscribers[signal] = s
	return s
}

func (r *Pool[T]) connect(ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, true)

	newPubSub := sub.pubsub == nil
	if newPubSub {
		sub.pubsub, err = r.client.Subscribe(ctx, signal)
	}

	sub.add(recv)

	return err
}

func (r *Pool[T]) clear(ctx context.Context, signal string) error {
	if contextIs(ctx, "disconnect", signal, r) {
		return nil
	}

	ctx = contextWith(ctx, "disconnect", signal, r)

	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, false)
	if sub == nil || sub.receivers == nil || sub.receivers.Len() == 0 {
		return nil
	}

	for id, recv := range sub.receivers.Iterator() {
		err := recv.Disconnect(ctx)
		if err != nil {
			return signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q", id,
			)
		}
	}

	sub.clear()

	return sub.check(signal)
}

func (r *Pool[T]) disconnect(ctx context.Context, sig *signal[T], recv signals.Receiver[T]) error {
	if contextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = contextWith(ctx, "disconnect", sig.name, r)

	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(sig.name, false)
	if sub == nil || sub.receivers == nil || sub.receivers.Len() == 0 {
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

func (r *Pool[T]) decodeMessage(_ context.Context, data []byte) (msg *Message, sentVal T, err error) {
	payload := new(Message)
	err = r.encoder.Decode(bytes.NewReader(data), payload)
	if err != nil {
		return nil, sentVal, err
	}

	val := new(T)
	err = r.encoder.Decode(bytes.NewReader(payload.Data), val)
	if err != nil {
		return payload, sentVal, err
	}

	return payload, *val, err
}

func (r *Pool[T]) newMessage(ctx context.Context, topic string, value T) (message *Message, err error) {
	var data []byte
	if any(value) != nil {
		data, err = r.encoder.EncodeBytes(value)
		if err != nil {
			return nil, err
		}
	}

	message = new(Message{
		Channel: topic,
		Sender:  r.inst,
		Data:    data,
		Meta:    msgMetaFromContext(ctx, r),
	})

	if message.Meta == nil {
		message.Meta = make(map[string]any)
	}

	if maker, ok := r.client.(PubSubMsgMaker); ok {
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
	messageContextKey     = newKey[struct{}]("pubsub.MessageFromContext", "", nil)
	messageMetaContextKey = newKey[struct{}]("pubsub.msgMetaFromContext", "", nil)
)

func MessageFromContext(ctx context.Context) *Message {
	var v, _ = ctx.Value(messageContextKey).(*Message)
	return v
}

func PoolFromContext[T any](ctx context.Context) PubSubPool[T] {
	var v, _ = ctx.Value(poolContextKey).(PubSubPool[T])
	return v
}

func contextWithMessage(ctx context.Context, msg *Message) context.Context {
	return context.WithValue(ctx, messageContextKey, msg)
}

func contextWithPool[T any](ctx context.Context, pool *Pool[T]) context.Context {
	return context.WithValue(ctx, poolContextKey, pool)
}

func msgMetaFromContext[T any](ctx context.Context, pool *Pool[T]) (meta map[string]any) {
	var v, _ = ctx.Value(messageMetaContextKey).(func(context.Context, *Pool[T]) map[string]any)
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
