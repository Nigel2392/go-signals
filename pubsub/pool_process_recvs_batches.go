//go:build batches
// +build batches

package pubsub

import (
	"context"
	"slices"
	"sync"
	"unsafe"

	"github.com/Nigel2392/go-signals"
)

func (r *Pool[T]) processReceivers(ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(error)) {

	ctx = contextWithPool(ctx, r)

	var batchSize = signals.BatchSize(ctx)
	var batches = (len(receivers) + batchSize - 1) / batchSize

	var wg = new(sync.WaitGroup)
	var wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))

	wg.Add(batches)

	for batch := range slices.Chunk(receivers, batchSize) {
		go r.processBatch(ctx, wgPtr, sig, batch, val, callErr)
	}

	wg.Wait()
}

func (r *Pool[T]) processBatch(ctx context.Context, wg *sync.WaitGroup, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(error)) {
	defer wg.Done()

	for _, receiver := range receivers {
		if r.closed.Load() || ctx.Err() != nil {
			return
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			callErr(signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q:", receiver.ID(),
			))
		}
	}
}

//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}
