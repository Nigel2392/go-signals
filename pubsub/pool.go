package pubsub

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"sync"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/omap"
	"github.com/Nigel2392/go-signals/internal/spinner"
	"github.com/Nigel2392/go-signals/internal/subscriber"
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
	subscribers map[string]*subscriber.Subscriber[T]

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(context.Context, *Pool[T], error)
}

func defaultPoolError[T any](ctx context.Context, p *Pool[T], err error) {
	log.Printf("error in pool %T: %v", p.MustClient(ctx), err)
}

func New[T any](clientCtx context.Context, pubsub any, opts ...PoolOption) *Pool[T] {
	pool := &Pool[T]{
		BasePool:    NewBasePool[*Pool[T]](clientCtx, pubsub),
		signals:     make(map[string]*signal[T]),
		subscribers: make(map[string]*subscriber.Subscriber[T]),
	}

	pool.BasePool.WithReference(pool)

	for _, opt := range opts {
		opt((*Pool[T])(pool))
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

func GoNew[T any](ctx context.Context, pubsub any, opts ...PoolOption) *Pool[T] {
	pool := New[T](ctx, pubsub, opts...)
	err := pool.BasePool.OnClientInit(ctx, pool.goNew)
	if err != nil {
		panic(err)
	}

	return pool
}

func (p *Pool[T]) goNew(ctx context.Context, _ PubSub) error {
	go func() {
		for err := range GoLoop(ctx, p, 16) {
			p.callErr(ctx, err)
		}
	}()
	return nil
}

func (p *Pool[T]) WithOnError(fn func(context.Context, *Pool[T], error)) {
	p.onErr = fn
}

func (r *Pool[T]) Send(ctx context.Context, topic string, value T) error {
	return r.BasePool.Send(ctx, topic, value)
}

func (r *Pool[T]) NewSignal(_ context.Context, name string) signals.Signal[T] {
	r.Mu.Lock()
	defer r.Mu.Unlock()
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
	//		return r.waitLoop(ctx, r.Data)
	//	}
	//
	//	func (r *Pool[T]) waitLoop(ctx context.Context, data <-chan Message) iter.Seq2[Handler[*Pool[T], T], error] {
	wg := sync.WaitGroup{}
	wg.Add(1)

	var err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, ps PubSub) error {
		wg.Done()
		return nil
	})
	if err != nil {
		return func(yield func(Handler[*Pool[T], T], error) bool) {
			yield(Handler[*Pool[T], T]{}, err)
		}
	}

	return func(yield func(Handler[*Pool[T], T], error) bool) {
		wg.Wait()

		if r.Exit != nil {
			panic(signals.ErrUnsupported.Wrap(
				"Pool.Loop() can only be called when in the stopped state",
			))
		}

		r.Exit = make(chan struct{})

		var doneCh = ctx.Done()
		for {
			var (
				handler Handler[*Pool[T], T]
				ok      bool
				err     error
			)

			if r.Data == nil {
				handler, ok, err = r.tryCycle(ctx)
			} else {
				handler, ok, err = r.cycle(ctx, doneCh, false)
			}

			if ok || err != nil {
				if !yield(handler, err) {
					break
				}
			}
			if !ok {
				break
			}
		}
	}
}

func (r *Pool[T]) Cycle(ctx context.Context, resend bool) (Processor, error) {
	if !r.BasePool.ClientWasSetup() {
		_, err := r.BasePool.Client(ctx) // init lazy clients
		if err != nil {
			return nil, signals.ErrPool.WithCause(err).Wrap("error during client setup")
		}
	}

	var (
		handler Handler[*Pool[T], T]
		ok      bool
		err     error
	)

	if r.Data == nil {
		if resend {
			panic(errors.New("cannot resend data when pool is in asynchronous mode"))
		}

		handler, ok, err = r.tryCycle(ctx)
	} else {
		handler, ok, err = r.cycle(ctx, ctx.Done(), resend)
	}

	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, ErrPoolClosed
	}

	return handler, nil
}

type tryCycleSnapshot[T any] struct {
	name       string
	sig        *signal[T]
	subscriber *subscriber.Subscriber[T]
}

func (r *Pool[T]) tryCycle(ctx context.Context) (handler Handler[*Pool[T], T], ok bool, err error) {

	r.Mu.RLock()

	snapShots := make([]tryCycleSnapshot[T], 0, len(r.subscribers))
	for k, sub := range r.subscribers {
		sig, ok := r.signals[k]
		if !ok {
			continue
		}

		snapShots = append(snapShots, tryCycleSnapshot[T]{
			name:       k,
			sig:        sig,
			subscriber: sub,
		})
	}

	r.Mu.RUnlock()

	var spin spinner.Spinner
	for {
	keyLoop:
		for _, snapshot := range snapShots {

			// rebuild subscriber cache if required
			// allows for better concurrency
			if snapshot.subscriber.Dirty.Load() {
				r.Mu.Lock()
				snapshot.subscriber.Undirtify()
				r.Mu.Unlock()
				snapshot.subscriber.Dirty.Store(false)
			}

			// see if we should exit the loop
			if r.Closed.Load() {
				return handler, false, errors.New("pool is closed (r.Closed == true)")
			}

			if err := ctx.Err(); err != nil {
				if errors.Is(err, context.Canceled) {
					return handler, false, nil
				}

				return handler, false, err
			}

			// try to receive the data
			payload, hasMessage := snapshot.subscriber.Pubsub.TryReceive()
			if !hasMessage {
				continue keyLoop // Queue empty, move to next subscriber
			}

			msg, val, err := r.decodeMessage[T](ctx, payload)
			if err != nil {
				return handler, true, err
			}

			handler.Value = val
			handler.Signal = snapshot.sig
			handler.Receivers = snapshot.subscriber.Cached
			handler.Message = msg
			handler.BasePool = r.BasePool

			return handler, true, nil
		}

		spin.Spin()
	}
}

func (r *Pool[T]) cycle(ctx context.Context, doneCh <-chan struct{}, resend bool) (h Handler[*Pool[T], T], ok bool, err error) {
	var payload Message
	select {
	case payload, ok = <-r.Data:
		if !ok {
			return
		}

	case <-r.Exit:
		return h, false, nil

	case <-doneCh:
		err = ctx.Err()
		if !errors.Is(err, context.Canceled) {
			return h, false, err
		}
		return h, false, nil
	}

	if resend {
		r.Data <- payload
	}

	if payload.Error != nil {
		return h, true, err
	}

	// retrieve subscriber object and signal
	r.Mu.RLock()
	sub, ok := r.subscribers[payload.Channel]
	if !ok {
		r.Mu.RUnlock()
		return h, true, nil
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
		return h, true, nil
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

	// decode value to send to receivers
	message, val, err := r.decodeMessage[T](ctx, payload.Data)
	if err != nil {
		return h, true, err
	}

	// process
	handler := NewHandler[*Pool[T], T](r.BasePool)
	handler.Value = val
	handler.Signal = sig
	handler.Receivers = sub.Cached
	handler.Message = message
	return handler, true, nil
}

func (r *Pool[T]) callErr(ctx context.Context, err error) {
	r.onErr(ctx, (*Pool[T])(r), err)
}

func (r *Pool[T]) newSub(signal string, createIfNotExists bool) (*subscriber.Subscriber[T], bool) {
	s, ok := r.subscribers[signal]
	if ok {
		return s, false
	}

	if !createIfNotExists {
		return nil, false
	}

	s = &subscriber.Subscriber[T]{
		Receivers: omap.NewOrderedMap(0, signals.Receiver[T].ID),
	}
	r.subscribers[signal] = s
	return s, true
}

func (r *Pool[T]) connect(ctx context.Context, signal string, recv signals.Receiver[T]) (err error) {
	r.Mu.Lock()
	defer r.Mu.Unlock()

	sub, isNew := r.newSub(signal, true)
	if isNew {
		err = r.BasePool.OnClientInit(ctx, func(ctx context.Context, c PubSub) error {
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

func (r *Pool[T]) disconnect(ctx context.Context, sig *signal[T], recv signals.Receiver[T]) error {
	if ContextIs(ctx, "disconnect", sig.name, r) {
		return nil
	}

	ctx = ContextWith(ctx, "disconnect", sig.name, r)

	r.Mu.Lock()
	defer r.Mu.Unlock()

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
