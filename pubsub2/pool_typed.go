package pubsub2

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"uuid"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
)

var (
	_ signals.SignalPool[int] = (*TPool[int])(nil)
	_ pubsub.PubSubPool[int]  = (*TPool[int])(nil)
)

type TPool[T any] Pool

func (r *TPool[T]) Pool() *Pool {
	return (*Pool)(r)
}

// [pubsub.PubSubPool]
func (r *TPool[T]) ID() uuid.UUID {
	return (*Pool)(r).ID()
}
func (r *TPool[T]) Loop(ctx context.Context) {
	(*Pool)(r).Loop(ctx)
}
func (r *TPool[T]) Close() {
	(*Pool)(r).Close()
}
func (r *TPool[T]) WaitLoop(ctx context.Context) iter.Seq2[pubsub.Handler[T], error] {
	chkTyp := reflect.TypeFor[T]()

	return func(yield func(pubsub.Handler[T], error) bool) {
		for handler, err := range (*Pool)(r).WaitLoop(ctx) {
			if err != nil && !yield(pubsub.Handler[T]{}, err) {
				break
			}

			// verify type matches desired type
			sigTyp := handler.Signal.(interface{ MsgType() reflect.Type }).MsgType()
			if sigTyp != chkTyp {
				panic(fmt.Sprintf("%s does not match required type %s", sigTyp, chkTyp))
			}

			// new handler object because the old one is of type Handler[any]
			newHandler := pubsub.NewHandler[T](r.BasePool)
			newHandler.Value = handler.Value.(T)
			newHandler.Message = handler.Message
			newHandler.Signal = (*signal[T])(handler.Signal.(*wrappedSignal[T]))

			// set [pubsub.Handler.ReceiversIter] instead of [pubsub.Handler.Receivers]
			// this saves a lot of b/op and some allocs (deepcopying slices)
			newHandler.ReceiversIter = func(yield func(signals.Receiver[T]) bool) {
				for _, r := range handler.Receivers {
					if !yield(TypedReceiver[T](r)) {
						break
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
