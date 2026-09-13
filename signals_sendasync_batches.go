//go:build batches
// +build batches

package signals

import (
	"context"
	"iter"
	"runtime"
	"slices"
	"sync"
	"unsafe"
)

var DEFAULT_BATCH_SIZE = 500

const BATCHES = true

//	func AsyncTransmitIter[T any](ctx context.Context, s Transmitter[T], chSizeSuggestion int, receivers iter.Seq[Receiver[T]], val T) <-chan error {
//		var (
//			batchSize = BatchSize(ctx)
//			errChan   = make(chan error, min(16, max(chSizeSuggestion, 1)))
//		)
//
//		go func() {
//			defer close(errChan)
//
//			var (
//				wg    = new(sync.WaitGroup)
//				wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))
//				batch = make([]Receiver[T], 0, batchSize)
//			)
//
//			for rec := range receivers {
//				batch = append(batch, rec)
//
//				if len(batch) >= batchSize {
//					// add to wg
//					wgPtr.Add(1)
//
//					// do work
//					go transmitBatch(ctx, wgPtr, errChan, s, batch, val)
//
//					// reset batch slice
//					batch = batch[:0]
//				}
//			}
//
//			// last batch
//			if len(batch) > 0 {
//				wgPtr.Add(1)
//
//				go transmitBatch(ctx, wgPtr, errChan, s, batch, val)
//			}
//
//			wgPtr.Wait()
//
//			runtime.KeepAlive(wg)
//
//		}()
//
//		return errChan
//	}
//
//	func AsyncTransmit[T any](ctx context.Context, s Transmitter[T], recvs []Receiver[T], value T) <-chan error {
//		// Check if there are any receivers.
//		if len(recvs) == 0 {
//			return nil
//		}
//
//		var (
//			batchSize = BatchSize(ctx)
//			batches   = (len(recvs) + batchSize - 1) / batchSize
//			chSize    = len(recvs) * 10 / 100
//		)
//
//		if chSize == 0 || chSize > batches {
//			chSize = batches
//		}
//
//		var errChan = make(chan error, chSize)
//		go func() {
//			defer close(errChan)
//
//			var (
//				wg    = new(sync.WaitGroup)
//				wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))
//			)
//
//			wgPtr.Add(batches)
//
//			for batch := range slices.Chunk(recvs, batchSize) {
//				go transmitBatch(ctx, wgPtr, errChan, s, batch, value)
//			}
//
//			wgPtr.Wait()
//
//			runtime.KeepAlive(wg)
//		}()
//
//		return errChan
//	}

func AsyncReceiveIter[T any](ctx context.Context, s Signal[T], chSizeSuggestion int, receivers iter.Seq[Receiver[T]], val T) <-chan error {
	var (
		batchSize = BatchSize(ctx)
		errChan   = make(chan error, min(16, max(chSizeSuggestion, 1)))
	)

	go func() {
		defer close(errChan)

		var (
			wg    = new(sync.WaitGroup)
			wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))
			batch = make([]Receiver[T], 0, batchSize)
		)

		for rec := range receivers {
			batch = append(batch, rec)

			if len(batch) >= batchSize {
				// add to wg
				wgPtr.Add(1)

				// do work
				go processBatch(ctx, wgPtr, errChan, s, batch, val)

				// reset batch slice
				batch = batch[:0]
			}
		}

		// last batch
		if len(batch) > 0 {
			wgPtr.Add(1)

			go processBatch(ctx, wgPtr, errChan, s, batch, val)
		}

		wgPtr.Wait()

		runtime.KeepAlive(wg)

	}()

	return errChan
}

func AsyncReceive[T any](ctx context.Context, s Signal[T], recvs []Receiver[T], value T) <-chan error {
	// Check if there are any receivers.
	if len(recvs) == 0 {
		return nil
	}

	var (
		batchSize = BatchSize(ctx)
		batches   = (len(recvs) + batchSize - 1) / batchSize
		chSize    = len(recvs) * 10 / 100
	)

	if chSize == 0 || chSize > batches {
		chSize = batches
	}

	var errChan = make(chan error, chSize)
	go func() {
		defer close(errChan)

		var (
			wg    = new(sync.WaitGroup)
			wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))
		)

		wgPtr.Add(batches)

		for batch := range slices.Chunk(recvs, batchSize) {
			go processBatch(ctx, wgPtr, errChan, s, batch, value)
		}

		wgPtr.Wait()

		runtime.KeepAlive(wg)
	}()

	return errChan
}

//	func transmitBatch[T any](ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, signal Transmitter[T], list []Receiver[T], value T) {
//		defer wg.Done()
//
//		var errs []error
//		for _, receiver := range list {
//			err := signal.Transmit(ctx, value, receiver)
//			if err != nil {
//				if errs == nil {
//					errs = make([]error, 0, 4)
//				}
//				errs = append(errs, err)
//			}
//		}
//
//		if len(errs) > 0 {
//			errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
//		}
//	}

func processBatch[T any](ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, signal Signal[T], list []Receiver[T], value T) {
	defer wg.Done()

	var errs []error
	for _, receiver := range list {
		err := receiver.Receive(ctx, signal, value)
		if err != nil {
			if errs == nil {
				errs = make([]error, 0, 4)
			}
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
	}
}

//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}
