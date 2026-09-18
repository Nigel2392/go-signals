package pubsub

import (
	"context"
	"iter"

	"github.com/Nigel2392/go-signals"
)

var _ Processor = (*Handler[AbstractPool, any])(nil)

// Execute the receivers with the provided value
//
// Returns an error, which might contain multiple joined errors.
func ProcessNow[POOL any, T any](ctx context.Context, p POOL, s signals.Signal[T], receivers []signals.Receiver[T], message *Message, v T) error {
	ctx = contextWithPool(ctx, p)
	ctx = ContextWithMessage(ctx, message)

	var errs []error
	for _, receiver := range receivers {
		// err := receive(ctx, s, receiver, value)
		err := signals.Receive(ctx, s, receiver, v)
		if err != nil {
			errs = append(errs, signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
		}
	}

	if len(errs) > 0 {
		return signals.Error{Val: "error(s) while executing receivers", Errors: errs}
	}

	return nil
}

type ReceiversIter[T any] struct {
	Len       int
	Receivers iter.Seq[signals.Receiver[T]]
}

type Handler[POOLTYPE AbstractPool, T any] struct {
	Value         T
	Signal        signals.Signal[T]
	Receivers     []signals.Receiver[T]
	ReceiversIter ReceiversIter[T]
	Message       *Message

	BasePool *BasePool[POOLTYPE]
	// process sync.Once
}

func NewHandler[POOLTYPE AbstractPool, T any](basePool *BasePool[POOLTYPE]) Handler[POOLTYPE, T] {
	return Handler[POOLTYPE, T]{
		BasePool: basePool,
	}
}

// Execute the receivers with the provided value
//
// Allows for changing the value before it is sent to the receivers, as well as providing
// a custom [context.Context] with a possible deadline
func (r Handler[P, T]) Process(ctx context.Context) <-chan error {
	ctx = contextWithPool(ctx, r.BasePool.backref)
	ctx = ContextWithMessage(ctx, r.Message)

	if r.ReceiversIter.Receivers != nil {
		return signals.AsyncReceiveIter(
			ctx, r.Signal,
			r.ReceiversIter.Len,
			r.ReceiversIter.Receivers,
			r.Value,
		)
	}

	return signals.AsyncReceive(ctx, r.Signal, r.Receivers, r.Value)
}
