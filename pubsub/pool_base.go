package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"iter"
	"log"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pkg/logger"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
	"github.com/pkg/errors"
)

var (
	_ ConfigPool = (*BasePool[AbstractPool])(nil)
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
	log logger.Log
	b   *BasePool[POOLTYPE]
	cs  clientSetup
	Mu  *sync.RWMutex
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

	case func(context.Context, AbstractPool) PubSub:
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

func (r *BasePool[P]) p() *p[P] {
	return &r.P
}

func (r *BasePool[P]) WithAutoInitClient(b bool) {
	for {
		var (
			oldFlags = r.P.cs.flags.Load()
			newFlags uint32
		)

		if b {
			newFlags = oldFlags | bpf_autoInit
		} else {
			newFlags = oldFlags &^ bpf_autoInit
		}

		if r.P.cs.flags.CompareAndSwap(oldFlags, newFlags) {
			break
		}
	}
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

	if r.P.log == nil {
		r.P.log = StdPoolLog(r.backref)
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

func (p *BasePool[P]) WithLog(log logger.Log) {
	p.P.log = log
}

func (p *BasePool[P]) WithInstanceID(id uuid.UUID) {
	p.Inst = id
}

func (p *BasePool[P]) WithEncoder(enc encoder.Encoder) {
	p.Encoder = enc
}

func (r *BasePool[P]) Close[T any](subscribers map[string]*Sub[T]) error {
	r.P.Mu.RLock()
	defer r.P.Mu.RUnlock()

	r.Closed.Store(true)

	if r.Exit != nil {
		close(r.Exit)
	}

	if r.Data != nil {
		close(r.Data)
	}

	return r.P.close(subscribers)
}

func (r *BasePool[P]) Send[T any](ctx context.Context, topic string, value T) error {

	r.P.log.Printf(ctx, logger.DEBUG, "sending message for topic %q", topic)

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

func (r *p[P]) close[T any](subscribers map[string]*Sub[T]) error {

	for key, sub := range subscribers {
		if sub.Pubsub != nil {
			if err := sub.Pubsub.Close(); err != nil {
				return errors.Wrapf(err, "in subscriber %q", key)
			}
		}
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
			b.BindChannel(ctx, r.b.backref)
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
func (r *p[P]) WaitLoop[T any, SIGNAL PoolSignal[T]](ctx context.Context, subs map[string]*Sub[T], sigs map[string]SIGNAL) iter.Seq2[Handler[P, T], error] {
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

	wg.Wait()

	r.Mu.Lock()
	if r.b.Exit != nil {
		panic(signals.ErrUnsupported.Wrap(
			"Pool.Loop() can only be called when in the stopped state",
		))
	}

	r.b.Exit = make(chan struct{})
	r.Mu.Unlock()

	return func(yield func(Handler[P, T], error) bool) {
		var doneCh = ctx.Done()
		var yieldHandler = func(handler Handler[P, T], ok bool, err error) bool {
			if ok || err != nil {

				if errors.Is(err, ErrRetriesExceeded) {
					return ok
				}

				if !yield(handler, err) {
					return false
				}
			}

			if !ok {
				return false
			}

			return true
		}

	outer:
		for {
			if r.b.Data == nil {
				for res := range r.RetryCycleIter(ctx, 0, 0, subs, sigs) {
					if !yieldHandler(res.Handler, res.ContinueLoop, res.Error) {
						break outer
					}
				}

				continue
			}

			if !yieldHandler(r.ChanCycle(ctx, doneCh, 0, 0, subs, sigs, false)) {
				break
			}
		}
	}
}

// Cycle tries to pluck a value from the pool
func (r *p[P]) Cycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, tries int, subscribers map[string]*Sub[T], sigs map[string]SIGNAL, resend bool) iter.Seq2[Processor, error] {
	if !r.ClientWasSetup() {
		_, err := r.b.Client(ctx) // init lazy clients
		if err != nil {
			return func(yield func(Processor, error) bool) {
				yield(nil, signals.ErrPool.WithCause(err).Wrap("error during client setup"))
			}
		}
	}

	var iter iter.Seq[CycleResult[P, T]]
	if r.b.Data == nil {
		if resend {
			panic(errors.New("cannot resend data when pool is in asynchronous mode"))
		}

		iter = r.RetryCycleIter(ctx, tries, 0, subscribers, sigs)
	} else {
		iter = r.ChanCycleIter(ctx, ctx.Done(), tries, 0, subscribers, sigs, resend)
	}

	return func(yield func(Processor, error) bool) {
		for res := range iter {
			if res.Error != nil {
				if errors.Is(res.Error, ErrRetriesExceeded) {
					break
				}

				yield(res.Handler, res.Error)
				break
			}

			if !res.ContinueLoop {
				break
			}

			if !yield(res.Handler, nil) {
				break
			}
		}

	}
}

func (r *p[P]) RetryCycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, tries int, waitFor time.Duration, subscribers map[string]*Sub[T], signals map[string]SIGNAL) (Handler[P, T], bool, error) {
	for res := range r.RetryCycleIter(ctx, tries, waitFor, subscribers, signals) {
		return res.Handler, res.ContinueLoop, res.Error
	}
	return Handler[P, T]{}, false, nil
}

type CycleResult[POOLTYPE AbstractPool, VALUE any] struct {
	Handler      Handler[POOLTYPE, VALUE]
	Error        error
	ContinueLoop bool
}

type subSnapshot[VAL any, SIG PoolSignal[VAL]] struct {
	topic string
	sig   SIG
	sub   *Sub[VAL]
}

func (r *p[P]) RetryCycleIter[T any, SIGNAL PoolSignal[T]](ctx context.Context, tries int, waitFor time.Duration, subscribers map[string]*Sub[T], signals map[string]SIGNAL) iter.Seq[CycleResult[P, T]] {

	r.Mu.RLock()

	snapShots := make([]subSnapshot[T, SIGNAL], 0, len(subscribers))
	for k, sub := range subscribers {
		sig, ok := signals[k]
		if !ok {
			continue
		}

		snapShots = append(snapShots, subSnapshot[T, SIGNAL]{
			topic: k,
			sig:   sig,
			sub:   sub,
		})
	}

	r.Mu.RUnlock()

	if waitFor == 0 {
		waitFor = 100 * time.Microsecond
	}

	return func(yield func(CycleResult[P, T]) bool) {
		var (
			handler    Handler[P, T]
			failed     int
			yieldCount int
		)

		for {

			var ranSnapshots bool
			for _, snapshot := range snapShots {

				// rebuild subscriber cache if required
				// allows for better concurrency
				if snapshot.sub.dirty.Load() {
					r.Mu.Lock()
					snapshot.sub.undirtify()
					r.Mu.Unlock()
					snapshot.sub.dirty.Store(false)
				}

				// see if we should exit the loop
				if r.b.Closed.Load() {
					yield(CycleResult[P, T]{
						Error: ErrPoolClosed.WithCause(errors.New("(r.Closed == true)")),
					})
					return
				}

				if err := ctx.Err(); err != nil {
					r.log.Printf(ctx, logger.WARN, "context error during RetryCycleIter: %v", err)
					if errors.Is(err, context.Canceled) {
						yield(CycleResult[P, T]{})
						return
					}

					yield(CycleResult[P, T]{
						Error: ErrContext.WithCause(err),
					})
					return
				}

				// try to receive the data
			receiveLoop:
				for {
					payload, hasMessage := snapshot.sub.Pubsub.TryReceive()
					if !hasMessage {
						break receiveLoop
					}

					if err := r.log.Printf(ctx, logger.DEBUG, "message received for %q", snapshot.topic); err != nil {
						if !yield(CycleResult[P, T]{Error: err, ContinueLoop: true}) {
							return
						}
					}

					msg, val, err := r.decodeMessage[T](ctx, snapshot.sig.MsgType(), payload)
					if err != nil {
						if !yield(CycleResult[P, T]{Error: err, ContinueLoop: true}) {
							return
						}
					}

					handler.Value = val
					handler.Signal = snapshot.sig
					handler.Receivers = snapshot.sub.cached
					handler.Message = msg
					handler.BasePool = r.b

					if !yield(CycleResult[P, T]{Handler: handler, ContinueLoop: true}) {
						r.log.Println(ctx, logger.DEBUG, "loop broken")
						return
					}

					r.log.Printf(ctx, logger.DEBUG, "distributed message across %d receivers", len(handler.Receivers))

					yieldCount++
					ranSnapshots = true
				}
			}

			if ranSnapshots {
				// success
				// reset failures and yield
				failed = 0
				continue
			}

			failed++

			// always acts as a blocking
			// operation if it hasn't yielded any value
			if yieldCount == 0 {
				time.Sleep(waitFor)
				continue
			}

			if tries == 0 && failed > 3 {
				goto retriesExceeded
			}

			if tries > 0 && failed >= tries {
				goto retriesExceeded
			}

			time.Sleep(waitFor)
		}

	retriesExceeded:
		yield(CycleResult[P, T]{ContinueLoop: true, Error: ErrRetriesExceeded})
	}
}

func (r *p[P]) ChanCycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, doneCh <-chan struct{}, tries int, waitForNext time.Duration, subscribers map[string]*Sub[T], signals map[string]SIGNAL, resend bool) (Handler[P, T], bool, error) {
	for res := range r.ChanCycleIter(ctx, doneCh, tries, waitForNext, subscribers, signals, resend) {
		return res.Handler, res.ContinueLoop, res.Error
	}
	return Handler[P, T]{}, false, nil
}

func (r *p[P]) ChanCycleIter[T any, SIGNAL PoolSignal[T]](ctx context.Context, doneCh <-chan struct{}, tries int, waitFor time.Duration, subscribers map[string]*Sub[T], signals map[string]SIGNAL, resend bool) iter.Seq[CycleResult[P, T]] {
	if waitFor == 0 {
		waitFor = 100 * time.Microsecond
	}

	r.Mu.RLock()
	exit := r.b.Exit
	r.Mu.RUnlock()

	return func(yield func(CycleResult[P, T]) bool) {
		var (
			payload          Message
			ok               bool
			failed, yieldCnt int
			timer            = time.NewTimer(waitFor)
		)

		if !timer.Stop() {
			<-timer.C
		}

		for {
			if yieldCnt > 0 && ((tries == 0 && failed > 3) || (tries > 0 && tries <= failed)) {
				break
			}

			var timeoutCh <-chan time.Time

			// Use the timer ONLY if we are actively draining or if tries limit is strictly > 0.
			// If tries == 0 and we haven't got 1 message yet, timeoutCh stays nil so we block safely forever.
			if yieldCnt > 0 || tries > 0 {
				timer.Reset(waitFor)
				timeoutCh = timer.C
			}

			select {
			case payload, ok = <-r.b.Data:
				// Ensure timer is safely cleared since we beat it
				if timeoutCh != nil && !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if !ok {
					return
				}

				yieldCnt++

				// reset on success
				failed = 0

			case <-exit:
				return

			case <-doneCh:
				err := ctx.Err()
				if errors.Is(err, context.Canceled) {
					yield(CycleResult[P, T]{})
					return
				}
				yield(CycleResult[P, T]{Error: err})
				return

			case <-timeoutCh: // wait for ticker before counting as fail
				// always acts as a blocking
				// operation if it hasn't yielded any value
				if yieldCnt == 0 {
					continue
				}

				failed++

				if tries == 0 && failed > 3 {
					goto retriesExceeded
				}

				if tries > 0 && failed >= tries {
					goto retriesExceeded
				}

				continue
			}

			if resend {
				r.b.Data <- payload
				runtime.Gosched()
			}

			if payload.Error != nil {
				if !yield(CycleResult[P, T]{ContinueLoop: true, Error: payload.Error}) {
					return
				}
				continue
			}

			// retrieve subscriber object and signal
			r.Mu.RLock()
			sub, ok := subscribers[payload.Channel]
			if !ok {
				r.Mu.RUnlock()
				continue
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
				continue
			}

			r.Mu.RUnlock()

			// rebuild subscriber cache if required
			// allows for better concurrency
			if sub.dirty.Load() {
				r.Mu.Lock()
				sub.undirtify()
				r.Mu.Unlock()
				sub.dirty.Store(false)
			}

			// decode value to send to receivers
			message, val, err := r.decodeMessage[T](ctx, sig.MsgType(), payload.Data)
			if err != nil {
				if !yield(CycleResult[P, T]{ContinueLoop: true, Error: err}) {
					return
				}
				continue
			}

			if !yield(CycleResult[P, T]{
				ContinueLoop: true,
				Handler: Handler[P, T]{
					Value:     val,
					Signal:    sig,
					Receivers: sub.cached,
					Message:   message,
					BasePool:  r.b,
				}},
			) {
				return
			}
		}

	retriesExceeded:
		yield(CycleResult[P, T]{ContinueLoop: true, Error: ErrRetriesExceeded})
	}
}
