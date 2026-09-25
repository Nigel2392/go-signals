package pubsub

import (
	"context"
	"iter"

	"github.com/Nigel2392/errors"
	"github.com/Nigel2392/go-signals"
)

var _ Processor = (*Handler[AbstractPool, any])(nil)

type ReceiversIter[T any] struct {
	Len       int
	Receivers iter.Seq[signals.Receiver[T]]
}

type Handler[POOLTYPE AbstractPool, T any] struct {
	Value         T
	Signal        PoolSignal[T]
	Receivers     []signals.Receiver[T]
	ReceiversIter ReceiversIter[T]
	Message       *Message

	BasePool *BasePool[POOLTYPE]
	// process sync.Once
}

// Execute the receivers with the provided value
//
// Returns an error, which might contain multiple joined errors.
func (r Handler[P, T]) ProcessNow(ctx context.Context) (err error) {
	var errs []error
	ctx = contextWithPool(ctx, r.BasePool.backref)
	ctx = ContextWithMessage(ctx, r.Message)

	if r.ReceiversIter.Receivers != nil {
		for receiver := range r.ReceiversIter.Receivers {
			// err := receive(ctx, s, receiver, value)
			err = signals.Receive(ctx, r.Signal, receiver, r.Value)
			if err != nil {
				errs = append(errs, signals.ReceiverError(receiver, err))
			}
		}
	} else {
		for _, receiver := range r.Receivers {
			// err := receive(ctx, s, receiver, value)
			err = signals.Receive(ctx, r.Signal, receiver, r.Value)
			if err != nil {
				errs = append(errs, signals.ReceiverError(receiver, err))
			}
		}
	}

	if len(errs) > 0 {
		return errors.Error{Message: "error(s) while executing receivers", Related: errs}
	}

	return nil
}

// Execute the receivers with the provided value
//
// Allows for changing the value before it is sent to the receivers, as well as providing
// a custom [context.Context] with a possible deadline
func (r Handler[P, T]) Process(ctx context.Context) <-chan error {

	if r.BasePool == nil {
		panic("handler not properly initialized")
	}

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
