package pubsub

import (
	"context"
	"iter"

	"github.com/Nigel2392/go-signals"
)

type Handler[T any] struct {
	Value         T
	Signal        signals.Signal[T]
	Receivers     []signals.Receiver[T]
	ReceiversIter iter.Seq[signals.Receiver[T]]
	Message       *Message

	basePool *BasePool
	// process sync.Once
}

func NewHandler[T any](basePool *BasePool) Handler[T] {
	return Handler[T]{
		basePool: basePool,
	}
}

// Execute the receivers with the provided value
//
// Allows for changing the value before it is sent to the receivers, as well as providing
// a custom [context.Context] with a possible deadline
func (r Handler[T]) Process(ctx context.Context) error {
	var errs []error
	// var mu sync.Mutex
	// r.process.Do(func() {

	ctx = ContextWithMessage(ctx, r.Message)
	if r.ReceiversIter != nil {
		r.basePool.processReceiversIter(ctx, r.Signal, r.ReceiversIter, r.Value, func(_ context.Context, err error) {
			errs = append(errs, err)
		})
	} else {
		r.basePool.processReceivers(ctx, r.Signal, r.Receivers, r.Value, func(_ context.Context, err error) {
			errs = append(errs, err)
		})
	}

	// })

	if len(errs) > 0 {
		return signals.Err("error while executing receivers", errs...)
	}

	return nil
}
