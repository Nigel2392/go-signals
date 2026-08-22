package pubsub

import (
	"bytes"
	"context"
	"log"
	"sync"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
	"github.com/elliotchance/orderedmap/v2"
)

type Pool[T any] struct {
	encoder encoder.Encoder

	mu          sync.RWMutex
	client      PubSub
	signals     map[string]*signal[T]
	subscribers map[string]*subscriber[T]
	onErr       func(*Pool[T], error)

	// managing the loop
	tickTime time.Duration
	exit     chan struct{}
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

	return pool
}

func (r *Pool[T]) Client() PubSub {
	return r.client
}

func (r *Pool[T]) Send(ctx context.Context, name string, value T) error {
	return r.NewSignal(ctx, name).Send(ctx, value)
}

func (r *Pool[T]) Close() {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.exit != nil {
		close(r.exit)
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

func (r *Pool[T]) Loop(ctx context.Context) {
	if r.exit != nil {
		panic("Loop() can only be called when in the stopped state")
	}

	tick := time.NewTicker(r.tickTime)
	r.exit = make(chan struct{})

loop:
	for {
		select {
		case <-tick.C:
			if r.Work(ctx) {
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

func (r *Pool[T]) Work(ctx context.Context) (stop bool) {
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
			select {
			case <-ctx.Done():
				r.callErr(ctx.Err())
				return true
			case <-r.exit:
				return true
			default:
			}

			payload, hasMessage := sub.pubsub.TryReceive()
			if !hasMessage {
				break drainLoop // Queue empty, move to next subscriber
			}

			r.decodeAndSend(ctx, sig, sub, payload)
		}
	}

	return false
}

func (r *Pool[T]) decodeAndSend(ctx context.Context, sig signals.Signal[T], sub *subscriber[T], payload []byte) {
	val := new(T)
	err := r.encoder.Decode(bytes.NewReader(payload), val)
	if err != nil {
		r.callErr(err)
		return
	}

	go r.processReceivers(ctx, sig, sub._cached, *val)
}

func (r *Pool[T]) processReceivers(ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T) {
receiverLoop:
	for _, receiver := range receivers {

		select {
		case <-ctx.Done():
			// do not call onErr, it will be handled by next loop iteration
			return
		case <-r.exit:
			return
		default:
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			r.callErr(signals.ErrReceiver.WithCause(err).Wrapf(
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

func (r *Pool[T]) send(ctx context.Context, topic string, v T) error {
	data, err := r.encoder.EncodeBytes(v)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T to gob value", v,
		)
	}

	err = r.client.Publish(ctx, topic, data)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not publish %T", v,
		)
	}

	return nil
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
