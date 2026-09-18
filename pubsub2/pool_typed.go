package pubsub2

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"unsafe"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
)

var (
	_ signals.SignalPool[int]             = (*TPool[int])(nil)
	_ pubsub.AbstractPool                 = (*TPool[int])(nil)
	_ pubsub.PubSubPool[int, *TPool[int]] = (*TPool[int])(nil)
)

type TPool[T any] Pool

func (r *TPool[T]) Pool() *Pool {
	return (*Pool)(r)
}

// [pubsub.PubSubPool]
func (r *TPool[T]) ID() uuid.UUID {
	return (*Pool)(r).ID()
}
func (r *TPool[T]) Close() {
	(*Pool)(r).Close()
}

func (r *TPool[T]) Cycle(ctx context.Context, resend bool) (pubsub.Processor, error) {
	return (*Pool)(r).Cycle(ctx, resend)
}

// custom waitloop handling, change from pubsub.Handler[any] to pubsub.Handler[T]
func (r *TPool[T]) WaitLoop(ctx context.Context) iter.Seq2[pubsub.Handler[*TPool[T], T], error] {
	chkTyp := reflect.TypeFor[T]()

	return func(yield func(pubsub.Handler[*TPool[T], T], error) bool) {
		for handler, err := range (*Pool).WaitLoop((*Pool)(r), ctx) {
			if err != nil && !yield(pubsub.Handler[*TPool[T], T]{}, err) {
				break
			}

			// verify type matches desired type
			if sigTyp := handler.Signal.MsgType(); sigTyp != chkTyp {
				panic(fmt.Sprintf("%s does not match required type %s", sigTyp, chkTyp))
			}

			// new handler object because the old one is of type Handler[any]
			//
			// pretty sure the below conversion is safe (tests pass)
			// since the underlying type of [pubsub.BasePool]'s POOLTYPE == *Pool == *TPool
			// newHandler := pubsub.NewHandler[*TPool[T], T]((*pubsub.BasePool[*TPool[T]])(unsafe.Pointer(r.BasePool)))
			newHandler := pubsub.Handler[*TPool[T], T]{
				BasePool: (*pubsub.BasePool[*TPool[T]])(unsafe.Pointer(r.BasePool)),
				Signal:   (*signal[T])(handler.Signal.(*wrappedSignal[T])),
				Message:  handler.Message,
				Value:    handler.Value.(T),
			}

			// set [pubsub.Handler.ReceiversIter] instead of [pubsub.Handler.Receivers]
			// this saves a lot of b/op and some allocs (deepcopying slices)
			newHandler.ReceiversIter.Len = len(handler.Receivers)
			newHandler.ReceiversIter.Receivers = func(yield func(signals.Receiver[T]) bool) {
			receiverLoop:
				for _, r := range handler.Receivers {
					switch s := r.(type) {
					case *ifaceReceiver[T]:
						if !yield(s.Receiver) {
							break receiverLoop
						}
					case *wrappedReceiver[T]:
						if !yield((*receiver[T])(s)) {
							break receiverLoop
						}
					case unwrapper[signals.Receiver[T]]:
						if !yield(s.Unwrap()) {
							break receiverLoop
						}
					case unwrapper[*receiver[T]]:
						if !yield(s.Unwrap()) {
							break receiverLoop
						}
					default:
						panic(fmt.Sprintf("cannot unwrap %T into %s", s, reflect.TypeFor[signals.Receiver[T]]()))
					}
				}
			}

			if !yield(newHandler, nil) {
				break
			}
		}
	}
}

// [pubsub.ChannelBinder]
func (r *TPool[T]) Client(ctx context.Context) (pubsub.PubSub, error) {
	return (*Pool)(r).Client(ctx)
}
func (r *TPool[T]) Channel(ctx context.Context) chan pubsub.Message {
	return (*Pool)(r).Channel(ctx)
}
func (r *TPool[T]) SetChannel(ctx context.Context, ch chan pubsub.Message) {
	(*Pool)(r).SetChannel(ctx, ch)
}

// [signals.SignalPool]
func (r *TPool[T]) Send(ctx context.Context, topic string, value T) error {
	return (*Pool)(r).Send(ctx, topic, value)
}
func (r *TPool[T]) Get(ctx context.Context, name string) signals.Signal[T] {
	return (*Pool)(r).Get[T](ctx, name)
}
func (r *TPool[T]) NewSignal(ctx context.Context, name string) signals.Signal[T] {
	return (*Pool)(r).NewSignal[T](ctx, name)
}
