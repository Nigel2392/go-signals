//go:build batches
// +build batches

package pubsub

import (
	"context"
	"iter"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"github.com/Nigel2392/go-signals"
)

func (r *BasePool) processReceiversIter[T any](ctx context.Context, sig signals.Signal[T], receivers iter.Seq[signals.Receiver[T]], val T, callErr func(context.Context, error)) {
	ctx = contextWithPool(ctx, r)
	var batchSize = signals.BatchSize(ctx)
	var wg = new(sync.WaitGroup)
	var wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))

	var batch = make([]signals.Receiver[T], 0, batchSize)
	for rec := range receivers {
		batch = append(batch, rec)

		if len(batch) >= batchSize {
			// add to wg
			wgPtr.Add(1)

			// do work
			go r.processBatch(ctx, wgPtr, sig, batch, val, callErr)

			// reset batch slice
			batch = batch[:0]
		}
	}

	// last batch
	if len(batch) > 0 {
		wgPtr.Add(1)
		go r.processBatch(ctx, wgPtr, sig, batch, val, callErr)
	}

	wgPtr.Wait()

	runtime.KeepAlive(wg)
}

func (r *BasePool) processReceivers[T any](ctx context.Context, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {

	ctx = contextWithPool(ctx, r)

	var batchSize = signals.BatchSize(ctx)
	var batches = (len(receivers) + batchSize - 1) / batchSize

	var wg = new(sync.WaitGroup)
	var wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))

	wgPtr.Add(batches)

	for batch := range slices.Chunk(receivers, batchSize) {
		go r.processBatch(ctx, wgPtr, sig, batch, val, callErr)
	}

	wgPtr.Wait()

	runtime.KeepAlive(wg)
}

func (r *BasePool) processBatch[T any](ctx context.Context, wg *sync.WaitGroup, sig signals.Signal[T], receivers []signals.Receiver[T], val T, callErr func(context.Context, error)) {
	defer wg.Done()

	for _, receiver := range receivers {
		if r.Closed.Load() || ctx.Err() != nil {
			return
		}

		err := receiver.Receive(ctx, sig, val)
		if err != nil {
			callErr(ctx, signals.ErrReceiver.WithCause(err).Wrapf(
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
