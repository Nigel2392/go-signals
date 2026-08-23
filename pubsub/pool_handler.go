package pubsub

import (
	"context"

	"github.com/Nigel2392/go-signals"
)

type Handler[T any] struct {
	Value     T
	Signal    signals.Signal[T]
	Receivers []signals.Receiver[T]
	Message   *Message

	pool *Pool[T]
	// process sync.Once
}

// Execute the receivers with the provided value
//
// Allows for changing the value before it is sent to the receivers, as well as providing
// a custom [context.Context] with a possible deadline
func (r *Handler[T]) Process(ctx context.Context) error {
	var errs []error
	// r.process.Do(func() {

	ctx = contextWithMessage(ctx, r.Message)

	r.pool.processReceivers(ctx, r.Signal, r.Receivers, r.Value, func(err error) {
		errs = append(errs, err)
	})

	// })

	if len(errs) > 0 {
		return signals.Err("error while executing receivers", errs...)
	}

	return nil
}
