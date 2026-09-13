package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

var (
	_ ChannelBinder = (*BasePool[ChannelBinder])(nil)
	_ ConfigPool    = (*BasePool[ChannelBinder])(nil)
)

const (
	bpf_none     uint32 = iota
	bpf_wasSetup uint32 = 1 << iota
	bpf_autoInit
	bpf_clientBound
)

//
//	type recusionContext int // zero value is context key
//
//	func deeper(ctx context.Context) context.Context {
//		c, _ := ctx.Value(recusionContext(0)).(recusionContext)
//		c++
//		return context.WithValue(ctx, recusionContext(0), c)
//	}
//
//	func depthViolated(ctx context.Context, depth int) bool {
//		c, _ := ctx.Value(recusionContext(0)).(recusionContext)
//		return c >= recusionContext(depth)
//	}

type ProcessorBasePool[P ChannelBinder] BasePool[P]

func (p *ProcessorBasePool[P]) ProcessReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {
	(*BasePool[P])(p).processReceivers(ctx, sig, receivers, val, callErr)
}

type clientSetup struct {
	// the underlying interface that handles
	// data transmission and retrieval
	client     PubSub
	_getClient func(context.Context) PubSub

	// prevent races during initialization
	cmu   sync.Mutex
	flags atomic.Uint32

	// handle lazy initialisation of the client.
	onClientInit []func(context.Context, PubSub) error
}

type BasePool[POOLTYPE any] struct {
	cs clientSetup

	Mu *sync.RWMutex

	// inst provides the instance ID for this pool object.
	//
	// if none is provided through the options,
	// this is automatically generated with uuid.NewUUID.
	Inst uuid.UUID

	// function to encode any values published.
	//
	// the default encoder is JSON.
	Encoder encoder.Encoder

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

	backref POOLTYPE
}

type minimumPool interface {
	ID() uuid.UUID
	Close()
	ChannelBinder
}

func NewBasePool[POOLTYPE minimumPool](clientCtx context.Context, pubsub any) *BasePool[POOLTYPE] {
	pool := &BasePool[POOLTYPE]{
		Mu: &sync.RWMutex{},
	}

	switch c := pubsub.(type) {
	case PubSub:
		pool.cs.client = c

	case func() PubSub:
		pool.cs._getClient = func(ctx context.Context) PubSub {
			return c()
		}
	case func(context.Context) PubSub:
		pool.cs._getClient = func(ctx context.Context) PubSub {
			return c(ctx)
		}

	case func(context.Context, ChannelBinder) PubSub:
		pool.cs._getClient = func(ctx context.Context) PubSub {
			return c(ctx, pool.backref)
		}

	default:
		panic(fmt.Sprintf("unknown client type: %T", pubsub))
	}

	return pool
}

func (r *BasePool[P]) ClientWasSetup() bool {
	return r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup
}

func (r *BasePool[P]) OnClientInit(ctx context.Context, fn func(context.Context, PubSub) error) error {

	// fast path check
	if r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup {
		return fn(ctx, r.cs.client)
	}

	r.cs.cmu.Lock()

	// slow path check
	if r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup {
		r.cs.cmu.Unlock()
		return fn(ctx, r.cs.client)
	}

	r.cs.onClientInit = append(r.cs.onClientInit, fn)
	r.cs.cmu.Unlock()
	return nil
}

func (r *BasePool[P]) WithAutoInitClient(b bool) {
}

func (r *BasePool[P]) WithReference(ref P) {
	r.backref = ref
}

func (r *BasePool[P]) Initialize(ctx context.Context) error {
	if r.Encoder == nil {
		r.Encoder = encoder.NewJSONEncoder()
	}

	if r.TickTime == 0 {
		r.TickTime = time.Millisecond / 2
	}

	if (r.Inst == uuid.UUID{}) {
		r.Inst = uuid.New()
	}

	if r.cs.client != nil || r.cs.flags.Load()&bpf_autoInit == bpf_autoInit {
		return r.setupClient(ctx)
	}

	return nil
}

func (r *BasePool[P]) ID() uuid.UUID {
	return r.Inst
}

func (r *BasePool[P]) MustClient(ctx context.Context) PubSub {
	if err := r.setupClient(ctx); err != nil {
		panic(fmt.Errorf("error initialising client: %w", err))
	}
	return r.cs.client
}

func (r *BasePool[P]) Client(ctx context.Context) (PubSub, error) {
	err := r.setupClient(ctx)
	return r.cs.client, err
}

func (r *BasePool[P]) SetChannel(ctx context.Context, ch chan Message) {
	r.Data = ch
}

func (r *BasePool[P]) Channel(ctx context.Context) chan Message {
	return r.Data
}

func (p *BasePool[P]) WithInstanceID(id uuid.UUID) {
	p.Inst = id
}

func (p *BasePool[P]) WithEncoder(enc encoder.Encoder) {
	p.Encoder = enc
}

func (p *BasePool[P]) WithTickDuration(t time.Duration) {
	p.TickTime = t
}

func (r *BasePool[P]) Close() {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	if r.Exit != nil {
		r.Closed.Store(true)
		close(r.Exit)
		r.Exit = nil
	}
}

func (r *BasePool[P]) Send[T any](ctx context.Context, topic string, value T) error {
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

func (r *BasePool[P]) newMessage[T any](ctx context.Context, topic string, value T) (message *Message, err error) {
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
		Meta:    MsgMetaFromContext(ctx, r.backref),
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

func (r *BasePool[P]) decodeMessage[T any](_ context.Context, data []byte) (msg *Message, sentVal T, err error) {
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

func (r *BasePool[P]) setupClient(ctx context.Context) error {
	if r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup {
		return nil
	}

	//ctx = deeper(ctx)
	//
	//if depthViolated(ctx, 3) {
	//	panic(fmt.Sprintf("recursion detected: %s", string(debug.Stack())))
	//}

	r.cs.cmu.Lock()
	defer r.cs.cmu.Unlock()

	// re-verify after lock
	if r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup {
		return nil
	}

	if r.cs.client == nil && r.cs._getClient == nil {
		panic("client is nil and _getClient is nil, cannot setup")
	}

	// ensure this is only ran once
	if r.cs.flags.Load()&bpf_clientBound != bpf_clientBound {
		if r.cs.client == nil && r.cs._getClient != nil {
			r.cs.client = r.cs._getClient(ctx)
			r.cs._getClient = nil
		}

		if b, ok := r.cs.client.(PubSubBinder); ok {
			b.BindChannel(ctx, r)
		}

		r.cs.flags.Or(bpf_clientBound)
	}

	for _, fn := range r.cs.onClientInit {
		if err := fn(ctx, r.cs.client); err != nil {
			return err
		}
	}

	r.cs.flags.Or(bpf_wasSetup)

	return nil
}

func (r *BasePool[P]) processReceiversIter[T any](ctx context.Context, sig signals.Signal[T], receivers iter.Seq[signals.Receiver[T]], val T, callErr func(context.Context, error)) {
	ctx = contextWithPool(ctx, r.backref)
	ch := signals.AsyncReceiveIter(ctx, sig, 0, receivers, val)
	for {
		select {
		case err, ok := <-ch:
			if !ok {
				return
			}
			callErr(ctx, err)
		case <-ctx.Done():
			callErr(ctx, ctx.Err())
		}
	}
}

func (r *BasePool[P]) processReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {
	ctx = contextWithPool(ctx, r.backref)

	ch := signals.AsyncReceive(ctx, sig, receivers, val)
	for {
		select {
		case err, ok := <-ch:
			if !ok {
				return
			}
			callErr(ctx, err)
		case <-ctx.Done():
			callErr(ctx, ctx.Err())
		}
	}
}
