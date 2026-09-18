package pubsub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"reflect"
	"sync"
	"sync/atomic"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/spinner"
	"github.com/Nigel2392/go-signals/internal/subscriber"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

var (
	_ ChannelBinder = (*BasePool[AbstractPool])(nil)
	_ ConfigPool    = (*BasePool[AbstractPool])(nil)
)

const (
	bpf_none     uint32 = iota
	bpf_wasSetup uint32 = 1 << iota
	bpf_autoInit
	bpf_clientBound
)

func defaultPoolError[POOLTYPE any](ctx context.Context, p POOLTYPE, err error) {
	log.Printf("error in pool %T: %v", p, err)
}

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

// internal struct to separate out methods from the basepool so
// they dont automatically become available when [BasePool] gets embedded in a [Pool]
type p[POOLTYPE AbstractPool] struct {
	b  *BasePool[POOLTYPE]
	cs clientSetup
	Mu *sync.RWMutex
}

type BasePool[POOLTYPE AbstractPool] struct {
	P p[POOLTYPE]

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

	// fast-path to check if currently in non-running state
	// selecting on `exit` makes the `Pool` slower by *orders of magnitude.*
	Closed atomic.Bool
	Exit   chan struct{}

	// special function to handle any
	// errors that occur during the async/loop process
	onErr func(context.Context, POOLTYPE, error)

	backref POOLTYPE
}

func NewBasePool[POOLTYPE AbstractPool](clientCtx context.Context, pubsub any) *BasePool[POOLTYPE] {
	pool := &BasePool[POOLTYPE]{}

	switch c := pubsub.(type) {
	case PubSub:
		pool.P.cs.client = c

	case func() PubSub:
		pool.P.cs._getClient = func(ctx context.Context) PubSub {
			return c()
		}
	case func(context.Context) PubSub:
		pool.P.cs._getClient = func(ctx context.Context) PubSub {
			return c(ctx)
		}

	case func(context.Context, ChannelBinder) PubSub:
		pool.P.cs._getClient = func(ctx context.Context) PubSub {
			return c(ctx, pool.backref)
		}

	default:
		panic(fmt.Sprintf("unknown client type: %T", pubsub))
	}

	pool.P.b = pool
	pool.P.Mu = &sync.RWMutex{}

	return pool
}

func (r *BasePool[P]) WithAutoInitClient(b bool) {
}

func (r *BasePool[P]) WithReference(ref P) {
	r.backref = ref
}

func (p *BasePool[P]) WithOnError(fn func(context.Context, P, error)) {
	p.onErr = fn
}

func (r *BasePool[P]) Initialize(ctx context.Context) error {
	if r.Encoder == nil {
		r.Encoder = encoder.NewJSONEncoder()
	}

	if r.onErr == nil {
		r.onErr = defaultPoolError
	}

	if (r.Inst == uuid.UUID{}) {
		r.Inst = uuid.New()
	}

	if r.P.cs.client != nil || r.P.cs.flags.Load()&bpf_autoInit == bpf_autoInit {
		return r.P.setupClient(ctx)
	}

	return nil
}

func (r *BasePool[P]) ID() uuid.UUID {
	return r.Inst
}

func (r *BasePool[P]) MustClient(ctx context.Context) PubSub {
	if err := r.P.setupClient(ctx); err != nil {
		panic(fmt.Errorf("error initialising client: %w", err))
	}
	return r.P.cs.client
}

func (r *BasePool[P]) Client(ctx context.Context) (PubSub, error) {
	err := r.P.setupClient(ctx)
	return r.P.cs.client, err
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

func (r *BasePool[P]) Close() {
	r.P.Mu.RLock()
	defer r.P.Mu.RUnlock()

	if r.Data != nil {
		close(r.Data)
	}

	if r.Exit != nil {
		r.Closed.Store(true)
		close(r.Exit)
	}
}

func (r *BasePool[P]) Send[T any](ctx context.Context, topic string, value T) error {
	message, err := r.P.newMessage(ctx, topic, value)
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

func (r *p[P]) decodeMessage[T any](_ context.Context, typ reflect.Type, data []byte) (msg *Message, sentVal T, err error) {
	payload := new(Message)
	err = r.b.Encoder.Decode(bytes.NewReader(data), payload)
	if err != nil {
		return nil, sentVal, err
	}

	val := reflect.New(typ)
	err = r.b.Encoder.Decode(bytes.NewReader(payload.Data), val.Interface())
	if err != nil {
		return payload, sentVal, err
	}

	return payload, val.Elem().Interface().(T), err
}

func (r *p[P]) ClientWasSetup() bool {
	return r.cs.flags.Load()&bpf_wasSetup == bpf_wasSetup
}

func (r *p[P]) OnClientInit(ctx context.Context, fn func(context.Context, PubSub) error) error {

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

func (p *p[P]) OnError(ctx context.Context, err error) {
	p.b.onErr(ctx, p.b.backref, err)
}

func (r *p[P]) newMessage[T any](ctx context.Context, topic string, value T) (message *Message, err error) {
	var data []byte
	if any(value) != nil {
		data, err = r.b.Encoder.EncodeBytes(value)
		if err != nil {
			return nil, err
		}
	}

	message = new(Message{
		Channel: topic,
		Sender:  r.b.Inst,
		Data:    data,
		Meta:    MsgMetaFromContext(ctx, r.b.backref),
	})

	if message.Meta == nil {
		message.Meta = make(map[string]any)
	}

	c, err := r.b.Client(ctx)
	if err != nil {
		return message, err
	}

	if maker, ok := c.(PubSubMsgMaker); ok {
		message = maker.MakeMessage(ctx, topic, message, true)
	}

	return message, nil
}

func (r *p[P]) setupClient(ctx context.Context) error {
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
			b.BindChannel(ctx, r.b)
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
func (r *p[P]) WaitLoop[T any, SIGNAL PoolSignal[T]](ctx context.Context, subs map[string]*subscriber.Subscriber[T], sigs map[string]SIGNAL) iter.Seq2[Handler[P, T], error] {
	wg := sync.WaitGroup{}
	wg.Add(1)

	var err = r.OnClientInit(ctx, func(ctx context.Context, ps PubSub) error {
		wg.Done()
		return nil
	})
	if err != nil {
		return func(yield func(Handler[P, T], error) bool) {
			yield(Handler[P, T]{}, err)
		}
	}

	return func(yield func(Handler[P, T], error) bool) {

		wg.Wait()

		if r.b.Exit != nil {
			panic(signals.ErrUnsupported.Wrap(
				"Pool.Loop() can only be called when in the stopped state",
			))
		}

		r.b.Exit = make(chan struct{})

		var doneCh = ctx.Done()
		for {
			var (
				handler Handler[P, T]
				ok      bool
				err     error
			)

			if r.b.Data == nil {
				handler, ok, err = r.RetryCycle(ctx, subs, sigs)
			} else {
				handler, ok, err = r.ChanCycle(ctx, doneCh, subs, sigs, false)
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

func (r *p[P]) Cycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*subscriber.Subscriber[T], sigs map[string]SIGNAL, resend bool) (Processor, error) {
	if !r.ClientWasSetup() {
		_, err := r.b.Client(ctx) // init lazy clients
		if err != nil {
			return nil, signals.ErrPool.WithCause(err).Wrap("error during client setup")
		}
	}

	var (
		handler Handler[P, T]
		ok      bool
		err     error
	)

	if r.b.Data == nil {
		if resend {
			panic(errors.New("cannot resend data when pool is in asynchronous mode"))
		}

		handler, ok, err = r.RetryCycle(ctx, subscribers, sigs)
	} else {
		handler, ok, err = r.ChanCycle(ctx, ctx.Done(), subscribers, sigs, resend)
	}

	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, ErrPoolClosed
	}

	return handler, nil
}

func (r *p[P]) RetryCycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*subscriber.Subscriber[T], signals map[string]SIGNAL) (handler Handler[P, T], ok bool, err error) {

	r.Mu.RLock()
	snapShots := subscriber.Snapshot(subscribers, signals)
	r.Mu.RUnlock()

	var spin spinner.Spinner
	for {
	keyLoop:
		for _, snapshot := range snapShots {

			// rebuild subscriber cache if required
			// allows for better concurrency
			if snapshot.Sub.Dirty.Load() {
				r.Mu.Lock()
				snapshot.Sub.Undirtify()
				r.Mu.Unlock()
				snapshot.Sub.Dirty.Store(false)
			}

			// see if we should exit the loop
			if r.b.Closed.Load() {
				return handler, false, errors.New("pool is closed (r.Closed == true)")
			}

			if err := ctx.Err(); err != nil {
				if errors.Is(err, context.Canceled) {
					return handler, false, nil
				}

				return handler, false, err
			}

			// try to receive the data
			payload, hasMessage := snapshot.Sub.Pubsub.TryReceive()
			if !hasMessage {
				continue keyLoop // Queue empty, move to next subscriber
			}

			msg, val, err := r.decodeMessage[T](ctx, snapshot.Sig.MsgType(), payload)
			if err != nil {
				return handler, true, err
			}

			handler.Value = val
			handler.Signal = snapshot.Sig
			handler.Receivers = snapshot.Sub.Cached
			handler.Message = msg
			handler.BasePool = r.b

			return handler, true, nil
		}

		spin.Spin()
	}
}

func (r *p[P]) ChanCycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, doneCh <-chan struct{}, subscribers map[string]*subscriber.Subscriber[T], signals map[string]SIGNAL, resend bool) (h Handler[P, T], ok bool, err error) {
	var payload Message
	select {
	case payload, ok = <-r.b.Data:
		if !ok {
			return
		}

	case <-r.b.Exit:
		return h, false, nil

	case <-doneCh:
		err = ctx.Err()
		if !errors.Is(err, context.Canceled) {
			return h, false, err
		}
		return h, false, nil
	}

	if resend {
		r.b.Data <- payload
	}

	if payload.Error != nil {
		return h, true, err
	}

	// retrieve subscriber object and signal
	r.Mu.RLock()
	sub, ok := subscribers[payload.Channel]
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

	sig, ok := signals[payload.Channel]
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
	message, val, err := r.decodeMessage[T](ctx, sig.MsgType(), payload.Data)
	if err != nil {
		return h, true, err
	}

	// process
	handler := NewHandler[P, T](r.b)
	handler.Value = val
	handler.Signal = sig
	handler.Receivers = sub.Cached
	handler.Message = message
	return handler, true, nil
}
