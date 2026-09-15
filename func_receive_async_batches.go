//go:build batches
// +build batches

package signals

import (
	"context"
	"iter"
	"runtime"
	"slices"
	"sync"
)

func init() {
	DEFAULT_BATCH_SIZE = 500
}

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

func AsyncReceiveIter[T any](ctx context.Context, s Signal[T], recvLen int, receivers iter.Seq[Receiver[T]], val T) <-chan error {

	var (
		chSizeSuggestion int = 16
		batchSize            = BatchSize(ctx)
	)

	if recvLen != 0 {
		batches := (recvLen + batchSize - 1) / batchSize
		chSizeSuggestion = batches
	}

	var (
		errChan = make(chan error, chSizeSuggestion)
		pool    = new(sync.Pool{
			New: func() any {
				l := make([]Receiver[T], 0, batchSize)
				return &l
			},
		})
	)

	go func() {
		defer close(errChan)

		var (
			wg = new(sync.WaitGroup)

			// as pointer, otherwise pool.Put cant
			// properly mark items as being reusable
			batch = pool.Get().(*[]Receiver[T])

			// batch = make([]Receiver[T], 0, batchSize)
		)

		var i int
		for rec := range receivers {
			*batch = append(*batch, rec)

			if len(*batch) >= batchSize {
				// add to wg
				wg.Add(1)

				// do work
				go processBatchPool(ctx, wg, errChan, s, batch, val, pool)
				// go processBatch(ctx, wg, errChan, s, batch, val)

				// reset batch slice
				batch = pool.Get().(*[]Receiver[T])
				// batch = make([]Receiver[T], 0, batchSize)

				// yield to other goroutines
				//
				// ensures the pool actually gets used,
				// i.e: a new slice doesn't get allocated
				// for **every** receiver
				if (i & 0x03) == 0 {
					runtime.Gosched()
				}

				i++
			}
		}

		// last batch
		if len(*batch) > 0 {
			wg.Add(1)

			go processBatchPool(ctx, wg, errChan, s, batch, val, pool)
			// go processBatch(ctx, wg, errChan, s, batch, val)
		}

		wg.Wait()
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

	var errChan = make(chan error, batches)
	go func() {
		defer close(errChan)

		var wg = new(sync.WaitGroup)

		wg.Add(batches)

		for batch := range slices.Chunk(recvs, batchSize) {
			go processBatch(ctx, wg, errChan, s, batch, value)
		}

		wg.Wait()
	}()

	return errChan
}

func processBatchPool[T any](ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, signal Signal[T], list *[]Receiver[T], value T, pool *sync.Pool) {
	defer func() {
		clear(*list)
		*list = (*list)[:0]
		pool.Put(list)
		wg.Done()
	}()

	var errs []error
	for _, receiver := range *list {
		err := Receive(ctx, signal, receiver, value)
		if err != nil {
			if errs == nil {
				errs = make([]error, 0, 4)
			}
			errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
				"Receiver(%s)", receiver.ID(),
			))
		}
	}

	if len(errs) > 0 {
		errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
	}
}

func processBatch[T any](ctx context.Context, wg *sync.WaitGroup, errChan chan<- error, signal Signal[T], list []Receiver[T], value T) {
	defer wg.Done()

	var errs []error
	for _, receiver := range list {
		err := Receive(ctx, signal, receiver, value)
		if err != nil {
			if errs == nil {
				errs = make([]error, 0, 4)
			}
			errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
				"Receiver(%s)", receiver.ID(),
			))
		}
	}

	if len(errs) > 0 {
		errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
	}
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
