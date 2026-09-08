package pubsub

import (
	"bytes"
	"context"
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

func (p *ProcessorBasePool) ProcessReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(error)) {
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
	client PubSub

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

	backref any
}

func NewBasePool(pubsub PubSub) BasePool {
	return BasePool{
		Mu:     &sync.RWMutex{},
		client: pubsub,
	}
}

func (r *BasePool) WithReference(ref any) {
	r.backref = ref
}

func (r *BasePool) Setup() {
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

func (r *BasePool) ID() uuid.UUID {
	return r.Inst
}

func (r *BasePool) Client() PubSub {
	return r.client
}

func (r *BasePool) SetChannel(ch chan Message) {
	r.Data = ch
}

func (r *BasePool) Channel() chan Message {
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
