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

	"github.com/Nigel2392/errors"
	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/internal/develop"
	"github.com/Nigel2392/go-signals/internal/spinner"
	"github.com/Nigel2392/go-signals/pkg/logger"
	"github.com/Nigel2392/go-signals/pubsub/encoder"
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

const (
	retriesAfterSuccess int = 3
	DEFAULT_CYCLE_PAUSE     = 100 * time.Microsecond
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

	if r.Exit == nil {
		r.Exit = make(chan struct{})
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

	if develop.DEVELOP {
		r.P.log.Printf(ctx, logger.DEBUG, "sending message for topic %q", topic)
	}

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
	data, err := r.b.Encoder.EncodeBytes(value)
	if err != nil {
		return nil, err
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

func (r *p[P]) Log() logger.Log {
	return r.log
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

	return func(yield func(Handler[P, T], error) bool) {
		var doneCh = ctx.Done()

		for {
			var iter CycleResultSeq[P, T]
			if r.b.Data == nil {
				iter = r.retryCycleIter(ctx, subs, sigs, CycleOptions{Flags: CF_NO_RETRY})
			} else {
				iter = r.chanCycleIter(ctx, doneCh, subs, sigs, CycleOptions{Flags: CF_NO_RETRY})
			}

			for res := range iter {
				if develop.DEVELOP {
					r.log.Printf(
						ctx, logger.DEBUG,
						"yielding result: (ok: %t | recvs: %d | channel: %d/%d | err: %v)",
						res.ContinueLoop, max(len(res.Handler.Receivers), res.Handler.ReceiversIter.Len),
						len(r.b.Data), cap(r.b.Data), res.Error,
					)
				}

				if res.ContinueLoop || res.Error != nil {
					if errors.Is(res.Error, ErrRetriesExceeded) {
						continue
					}

					if !yield(res.Handler, res.Error) {
						goto ending
					}
				}

				if !res.ContinueLoop {
					goto ending
				}
			}
		}

	ending:
		// if develop.DEVELOP {
		r.log.Printf(ctx, logger.DEBUG, "broken loop for pool %T(%s)", r.b.backref, r.b.Inst)
		// }
	}
}

type CycleResultSeq[P AbstractPool, T any] iter.Seq[CycleResult[P, T]]

func (i CycleResultSeq[P, T]) ProcessorSeq2(yield func(Processor, error) bool) {
	for res := range i {
		if res.Error != nil {
			if errors.Is(res.Error, ErrRetriesExceeded) {
				break
			}

			if !yield(res.Handler, res.Error) {
				break
			}
		}

		if !res.ContinueLoop {
			yield(res.Handler, ErrPoolClosed)
			break
		}

		if !yield(res.Handler, nil) {
			break
		}
	}
}

// Cycle tries to pluck a value from the pool as long as one is available.
//
// It will return after retrieving at least a single value, but will try to do it's best to drain
// any values currently stuck in the queue.
func (r *p[P]) Cycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*Sub[T], sigs map[string]SIGNAL, opts CycleOptions) error {
	var errs []error
	for res := range r.CycleIter(ctx, subscribers, sigs, opts) {
		if res.Error != nil {
			if errors.Is(res.Error, ErrRetriesExceeded) {
				break
			}
			return res.Error
		}

		if !res.ContinueLoop {
			return ErrPoolClosed
		}

		for err := range res.Handler.Process(ctx) {
			if err != nil {
				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			return errors.Error{Message: "error(s) while executing handlers", Related: errs}
		}
	}

	return nil
}

//	// Cycle tries to pluck a value from the pool as long as one is available.
//	//
//	// It will return after retrieving at least a single value, but will try to do it's best to drain
//	// any values currently stuck in the queue.
//	func (r *p[P]) Cycle[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*Sub[T], sigs map[string]SIGNAL, opts CycleOptions) error {
//		var errs []error
//
//		ctx = contextWithPool(ctx, r.b.backref)
//
//		for res := range r.CycleIter(ctx, subscribers, sigs, opts) {
//			if res.Error != nil {
//				if errors.Is(res.Error, ErrRetriesExceeded) {
//					break
//				}
//				return res.Error
//			}
//
//			if !res.ContinueLoop {
//				return ErrPoolClosed
//			}
//
//			if res.Handler.ReceiversIter.Receivers != nil {
//				for err := range signals.AsyncReceiveIter(
//					ContextWithMessage(ctx, res.Handler.Message), res.Handler.Signal,
//					res.Handler.ReceiversIter.Len,
//					res.Handler.ReceiversIter.Receivers,
//					res.Handler.Value,
//				) {
//					if err != nil {
//						errs = append(errs, err)
//					}
//				}
//			} else {
//				for err := range signals.AsyncReceive(ContextWithMessage(ctx, res.Handler.Message), res.Handler.Signal, res.Handler.Receivers, res.Handler.Value) {
//					if err != nil {
//						errs = append(errs, err)
//					}
//				}
//			}
//
//			if len(errs) > 0 {
//				return errors.Error{Message: "error(s) while executing handlers", Related: errs}
//			}
//		}
//
//		return nil
//	}

// CycleIter tries to pluck as many handlers as it can based on the provided options.
func (r *p[P]) CycleIter[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*Sub[T], sigs map[string]SIGNAL, opts CycleOptions) CycleResultSeq[P, T] {
	if !r.ClientWasSetup() {
		_, err := r.b.Client(ctx) // init lazy clients
		if err != nil {
			return func(yield func(CycleResult[P, T]) bool) {
				yield(CycleResult[P, T]{Error: signals.ErrPool.WithCause(err).Wrap("error during client setup")})
			}
		}
	}

	if r.b.Data == nil {
		if opts.Flags&CF_RESEND == CF_RESEND {
			panic(errors.New(signals.CodePoolError, "cannot resend data when pool is in asynchronous mode"))
		}

		return r.retryCycleIter(ctx, subscribers, sigs, opts)
	} else {
		return r.chanCycleIter(ctx, ctx.Done(), subscribers, sigs, opts)
	}
}

type CycleResult[POOLTYPE AbstractPool, VALUE any] struct {
	Handler[POOLTYPE, VALUE]
	Error        error
	ContinueLoop bool
}

type subSnapshot[VAL any, SIG PoolSignal[VAL]] struct {
	topic string
	sig   SIG
	sub   *Sub[VAL]
}

func (r *p[P]) retryCycleIter[T any, SIGNAL PoolSignal[T]](ctx context.Context, subscribers map[string]*Sub[T], signals map[string]SIGNAL, opts CycleOptions) CycleResultSeq[P, T] {

	if opts.WaitForNext == 0 {
		opts.WaitForNext = DEFAULT_CYCLE_PAUSE
	}

	retriesAfterSuccess := max(retriesAfterSuccess, opts.Tries)

	return func(yield func(CycleResult[P, T]) bool) {
		var (
			handler Handler[P, T]
			spin    spinner.Spinner
			failed  int
			success int
		)

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

		for {

			var yieldedSnapshots bool
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
					yield(CycleResult[P, T]{})
					return
				}

				if err := ctx.Err(); err != nil {

					if develop.DEVELOP {
						r.log.Printf(ctx, logger.WARN, "context error during retryCycleIter: %v", err)
					}

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

					if develop.DEVELOP {
						if err := r.log.Printf(ctx, logger.DEBUG, "message received for %q", snapshot.topic); err != nil {
							if !yield(CycleResult[P, T]{Error: err, ContinueLoop: true}) {
								return
							}
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
						if develop.DEVELOP {
							r.log.Println(ctx, logger.DEBUG, "loop broken")
						}
						return
					}

					if develop.DEVELOP {
						r.log.Printf(ctx, logger.DEBUG, "distributed message across %d receivers", len(handler.Receivers))
					}

					success++
					yieldedSnapshots = true
				}
			}

			if yieldedSnapshots {
				// success
				// reset failures and yield unless NoRetry is true
				failed = 0
				continue
			}

			failed++

			// always acts as a blocking
			// operation if it hasn't yielded any value
			// but do so without yielding to the OS
			if success == 0 {
				spin.Spin()
				continue
			}

			// above success count check ensures we have managed
			// to receive at least a single value and currently yielded none
			//
			// we are asked to return.
			if opts.Flags&CF_NO_RETRY == CF_NO_RETRY {
				goto retriesExceeded
			}

			if opts.Tries == 0 && failed > retriesAfterSuccess {
				goto retriesExceeded
			}

			if opts.Tries > 0 && failed >= opts.Tries {
				goto retriesExceeded
			}

			time.Sleep(opts.WaitForNext)
		}

	retriesExceeded:
		yield(CycleResult[P, T]{ContinueLoop: true, Error: ErrRetriesExceeded})

		//	if false {
		//		r.retryCycleIter(ctx, tries, waitFor, subscribers, signals)(yield)
		//		return
		//	}
	}
}

func (r *p[P]) chanCycleIter[T any, SIGNAL PoolSignal[T]](ctx context.Context, doneCh <-chan struct{}, subscribers map[string]*Sub[T], signals map[string]SIGNAL, opts CycleOptions) CycleResultSeq[P, T] {
	if opts.WaitForNext == 0 {
		opts.WaitForNext = DEFAULT_CYCLE_PAUSE
	}

	if r.b.Exit == nil {
		panic("exit channel is nil")
	}

	if doneCh == nil {
		panic("doneCh channel is nil")
	}

	retriesAfterSuccess := max(retriesAfterSuccess, opts.Tries)

	return func(yield func(CycleResult[P, T]) bool) {

		var (
			payload          Message
			ok               bool
			failed, yieldCnt int
			timer            = time.NewTimer(opts.WaitForNext)
			closeCh          chan struct{}
			timeoutCh        <-chan time.Time
		)

		for {
			if yieldCnt > 0 && ((opts.Tries == 0 && failed > retriesAfterSuccess) || (opts.Tries > 0 && opts.Tries <= failed)) {
				break
			}

			// Use the timer ONLY if we are actively draining or if tries limit is strictly > 0.
			// If tries == 0 and we haven't got 1 message yet, timeoutCh stays nil so we block safely forever.
			switch {
			case opts.Tries > 0:
				timer.Reset(opts.WaitForNext)
				timeoutCh = timer.C

			case yieldCnt > 0 && opts.Flags&CF_NO_RETRY == CF_NO_RETRY && closeCh == nil:
				// we are explicitly told not to retry for new values if it entails a waiting period.
				//
				// process all values currently in r.b.Data, return on the first fail encountered
				//
				// introducing this channel ensures we don't need to write a second (giant) select statement
				// in an if-block (functions as 'default' block in select statement)
				closeCh = make(chan struct{})
				close(closeCh)

			case yieldCnt > 0:
				timer.Reset(opts.WaitForNext)
				timeoutCh = timer.C
			}

			if develop.DEVELOP {
				r.log.Printf(ctx, logger.DEBUG, "waiting for result in pool %s...", r.b.Inst)
			}

			select {
			case payload, ok = <-r.b.Data:

			case <-closeCh:
				// go playground testing indicates order of receive
				// is not deterministic
				//
				// i.e: we need to check the data channel again
				// to be 100% sure that it does not currently old a value.
				select {
				case payload, ok = <-r.b.Data:
				default:
					goto retriesExceeded
				}

			case <-timeoutCh: // wait for ticker before counting as fail
				// always acts as a blocking
				// operation if it hasn't yielded any value
				if yieldCnt == 0 {
					continue
				}

				failed++

				if opts.Tries == 0 && failed > retriesAfterSuccess {
					goto retriesExceeded
				}

				if opts.Tries > 0 && failed >= opts.Tries {
					goto retriesExceeded
				}

				continue

			case <-r.b.Exit:
				// first yield until empty
				// then return on default case below
				select {
				case payload, ok = <-r.b.Data:
				default:
					yield(CycleResult[P, T]{})
					return
				}

			case <-doneCh:
				err := ctx.Err()
				if errors.Is(err, context.Canceled) {
					yield(CycleResult[P, T]{})
					return
				}
				yield(CycleResult[P, T]{Error: err})
				return
			}

			if !ok {
				yield(CycleResult[P, T]{})
				return
			}

			// Ensure timer is safely cleared since we beat it
			if timeoutCh != nil && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}

			yieldCnt++

			// reset on success
			failed = 0

			if opts.Flags&CF_RESEND == CF_RESEND {
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
				Value:        val,
				Signal:       sig,
				Receivers:    sub.cached,
				Message:      message,
				BasePool:     r.b,
			},
			) {
				return
			}
		}

	retriesExceeded:
		yield(CycleResult[P, T]{ContinueLoop: true, Error: ErrRetriesExceeded})
	}
}
