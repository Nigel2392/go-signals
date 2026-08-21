package redis

import (
	"context"
	"encoding/base64"
	"encoding/gob"
	"strings"
	"sync"
	"time"

	"github.com/Nigel2392/go-signals"
	"github.com/elliotchance/orderedmap/v2"
	"github.com/redis/go-redis/v9"
)

type RedisPool[T any] interface {
	signals.SignalPool[T]
	Loop(ctx context.Context, tickTime time.Duration, errCh chan<- error, exitCh <-chan struct{})
}

type redisPool[T any] struct {
	mu          sync.RWMutex
	client      redis.UniversalClient
	channelOpts []redis.ChannelOption
	signals     map[string]*signal[T]
	subscribers map[string]*subscriber[T]
}

func NewPool[T any](client redis.UniversalClient, channelOpts ...redis.ChannelOption) RedisPool[T] {
	return &redisPool[T]{
		mu:          sync.RWMutex{},
		client:      client,
		channelOpts: channelOpts,
		signals:     make(map[string]*signal[T]),
		subscribers: make(map[string]*subscriber[T]),
	}
}

func (r *redisPool[T]) NewSignal(ctx context.Context, name string) signals.Signal[T] {
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

func (r *redisPool[T]) Send(ctx context.Context, name string, value T) error {
	return r.NewSignal(ctx, name).Send(ctx, value)
}

func (r *redisPool[T]) Loop(ctx context.Context, tickTime time.Duration, errCh chan<- error, exitCh <-chan struct{}) {
	ticker := time.NewTicker(tickTime)

	for {
		select {
		case <-ticker.C:
			if r.loopQueue(ctx, errCh, exitCh) {
				return
			}
		case <-ctx.Done():
			errCh <- ctx.Err()
			return
		case <-exitCh:
			return
		}
	}
}

func (r *redisPool[T]) loopQueue(ctx context.Context, errCh chan<- error, exitCh <-chan struct{}) (stop bool) {
	if len(r.subscribers) == 0 {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for pubName, sub := range r.subscribers {

		if sub.receivers == nil || sub.receivers.Len() == 0 {
			continue
		}

		var msg *redis.Message
		var sig, ok = r.signals[pubName]
		if !ok {
			continue
		}

		select {
		case msg, ok = <-sub.receive:
		case <-ctx.Done():
			errCh <- ctx.Err()
			return true
		case <-exitCh:
			return true
		default:
			continue
		}

		if !ok || msg == nil {
			continue
		}

		enc := gob.NewDecoder(base64.NewDecoder(
			base64.StdEncoding,
			strings.NewReader(msg.Payload),
		))

		val := new(T)
		err := enc.Decode(val)
		if err != nil {
			errCh <- err
			continue
		}

	receiverLoop:
		for head := sub.receivers.Front(); head != nil; head = head.Next() {
			err := head.Value.Receive(ctx, sig, *val)
			if err != nil {
				errCh <- signals.ErrReceiver.WithCause(err).Wrapf(
					"receiver %q:", head.Value.ID(),
				)
				continue receiverLoop
			}
		}
	}

	return false
}

func (r *redisPool[T]) newSub(signal string, createIfNotExists bool) *subscriber[T] {
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

func (r *redisPool[T]) connect(ctx context.Context, signal string, recv signals.Receiver[T]) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, true)

	if sub.pubsub == nil {
		sub.pubsub = r.client.Subscribe(ctx, signal)
		sub.receive = sub.pubsub.Channel(r.channelOpts...)
	}

	sub.add(recv)
	return nil
}

func (r *redisPool[T]) clear(ctx context.Context, signal string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(signal, false)
	if sub == nil || sub.pubsub == nil {
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

	sub.receivers = orderedmap.NewOrderedMap[string, signals.Receiver[T]]()

	return sub.check(signal)
}

func (r *redisPool[T]) disconnect(ctx context.Context, sig *signal[T], recv signals.Receiver[T]) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := r.newSub(sig.name, false)
	if sub == nil || sub.pubsub == nil {
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
