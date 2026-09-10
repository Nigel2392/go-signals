//go:build !batches
// +build !batches

package pubsub

import (
	"context"
	"iter"

	"github.com/Nigel2392/go-signals"
)

func (r *BasePool) processReceiversIter[T any](ctx context.Context, sig signals.Signal[T], receivers iter.Seq[signals.Receiver[T]], val T, callErr func(context.Context, error)) {
	ctx = contextWithPool(ctx, r.backref)

receiverLoop:
	for receiver := range receivers {
		if r.Closed.Load() || ctx.Err() != nil {
			return
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			callErr(ctx, signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
			continue receiverLoop
		}
	}
}

func (r *BasePool) processReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {

	ctx = contextWithPool(ctx, r.backref)

receiverLoop:
	for _, receiver := range receivers {
		if r.Closed.Load() || ctx.Err() != nil {
			return
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			callErr(ctx, signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
			continue receiverLoop
		}
	}
}
