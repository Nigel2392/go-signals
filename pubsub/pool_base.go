package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type RWLocker interface {
	sync.Locker
	RLock()
	RUnlock()
}

type ProcessorBasePool BasePool

func (p *ProcessorBasePool) ProcessReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {
	(*BasePool)(p).processReceivers(ctx, sig, receivers, val, callErr)
}

type BasePool struct {
	Mu RWLocker

	// inst provides the instance ID for this pool object.
	//
	// if none is provided through the options,
	// this is automatically generated with uuid.NewUUID.
	Inst uuid.UUID

	// function to encode any values published.
	//
	// the default encoder is JSON.
	Encoder encoder.Encoder

	// the underlying interface that handles
	// data transmission and retrieval
	client     PubSub
	_getClient func(context.Context) PubSub

	wasSetup atomic.Bool

	// handle lazy initialisation of the client.
	onClientInit []func(context.Context, PubSub) error

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
	Data chan Message

	// managing the loop
	TickTime time.Duration // only used when in async/loop mode

	// fast-path to check if currently in non-running state
	// selecting on `exit` makes the `Pool` slower by *orders of magnitude.*
	Closed atomic.Bool
	Exit   chan struct{}

	backref ChannelBinder
}

func NewBasePool(pubsub any) *BasePool {
	pool := &BasePool{
		Mu: &sync.RWMutex{},
	}

	switch c := pubsub.(type) {
	case PubSub:
		pool.client = c

	case func() PubSub:
		pool._getClient = func(ctx context.Context) PubSub {
			return c()
		}
	case func(context.Context) PubSub:
		pool._getClient = func(ctx context.Context) PubSub {
			return c(ctx)
		}

	case func(context.Context, ChannelBinder) PubSub:
		pool._getClient = func(ctx context.Context) PubSub {
			return c(ctx, pool.backref)
		}

	default:
		panic(fmt.Sprintf("unknown client type: %T", pubsub))
	}

	return pool
}

func (r *BasePool) ClientWasSetup() bool {
	return r.client != nil
}

func (r *BasePool) OnClientInit(ctx context.Context, fn func(context.Context, PubSub) error) error {
	if r.ClientWasSetup() {
		return fn(ctx, r.client)
	}
	r.onClientInit = append(r.onClientInit, fn)
	return nil
}

func (r *BasePool) WithReference(ref ChannelBinder) {
	r.backref = ref
}

func (r *BasePool) Initialize() {
	if r.Encoder == nil {
		r.Encoder = encoder.NewJSONEncoder()
	}

	if r.TickTime == 0 {
		r.TickTime = time.Millisecond / 2
	}

	if (r.Inst == uuid.UUID{}) {
		r.Inst = uuid.New()
	}
}

func (r *BasePool) ID() uuid.UUID {
	return r.Inst
}

func (r *BasePool) setupClient(ctx context.Context) error {
	if r.client != nil && r.wasSetup.Load() {
		return nil
	}

	if r.client == nil && r._getClient == nil {
		panic("client is nil and _getClient is nil, cannot setup")
	}

	if r.client == nil && r._getClient != nil {
		r.client = r._getClient(ctx)
	}

	r.wasSetup.Store(true)

	if b, ok := r.client.(PubSubBinder); ok {
		b.BindChannel(ctx, r)
	}

	for _, fn := range r.onClientInit {
		if err := fn(ctx, r.client); err != nil {
			return err
		}
	}

	return nil
}

func (r *BasePool) MustClient(ctx context.Context) PubSub {
	if err := r.setupClient(ctx); err != nil {
		panic(fmt.Errorf("error initialising client: %w", err))
	}
	return r.client
}

func (r *BasePool) Client(ctx context.Context) (PubSub, error) {
	err := r.setupClient(ctx)
	return r.client, err
}

func (r *BasePool) SetChannel(ctx context.Context, ch chan Message) {
	r.Data = ch
}

func (r *BasePool) Channel(ctx context.Context) chan Message {
	return r.Data
}

func (p *BasePool) WithInstanceID(id uuid.UUID) {
	p.Inst = id
}

func (p *BasePool) WithEncoder(enc encoder.Encoder) {
	p.Encoder = enc
}

func (p *BasePool) WithTickDuration(t time.Duration) {
	p.TickTime = t
}

func (r *BasePool) decodeMessage[T any](_ context.Context, data []byte) (msg *Message, sentVal T, err error) {
	payload := new(Message)
	err = r.Encoder.Decode(bytes.NewReader(data), payload)
	if err != nil {
		return nil, sentVal, err
	}

	val := new(T)
	err = r.Encoder.Decode(bytes.NewReader(payload.Data), val)
	if err != nil {
		return payload, sentVal, err
	}

	return payload, *val, err
}
