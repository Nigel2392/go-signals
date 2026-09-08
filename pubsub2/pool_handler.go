package pubsub2

import (
	"context"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
)

type Handler struct {
	Value     any
	Signal    signals.Signal[any]
	Receivers []signals.Receiver[any]
	Message   *pubsub.Message

	pool *Pool
	// process sync.Once
}

// Execute the receivers with the provided value
//
// Allows for changing the value before it is sent to the receivers, as well as providing
// a custom [context.Context] with a possible deadline
func (r *Handler) Process(ctx context.Context) error {
	var errs []error
	// var mu sync.Mutex
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
