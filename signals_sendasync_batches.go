//go:build batches
// +build batches

package signals

import (
	"context"
	"slices"
	"sync"
	"unsafe"
)

var DEFAULT_BATCH_SIZE = 8

type batchSizeContextKey struct{}

func ContextWithBatchSize(ctx context.Context, size int) context.Context {
	return context.WithValue(ctx, batchSizeContextKey{}, size)
}

func (s *signal[T]) SendAsync(ctx context.Context, value T) chan error {
	recvs := s.getReceivers()

	// Check if there are any receivers.
	if len(recvs) == 0 {
		return nil
	}

	var batchSize = DEFAULT_BATCH_SIZE
	if bs, ok := ctx.Value(batchSizeContextKey{}).(int); ok {
		batchSize = bs
	}

	// Send the signal to each receiver.
	var batches = (len(recvs) + batchSize - 1) / batchSize
	var errChan chan error = make(chan error, batchSize)
	go func() {
		defer close(errChan)
		var wg = new(sync.WaitGroup)
		var wgPtr = (*sync.WaitGroup)(noescape(unsafe.Pointer(wg)))

		wg.Add(batches)

		for batch := range slices.Chunk(recvs, batchSize) {
			go processBatch(ctx, wgPtr, errChan, s, batch, value)
		}

		wg.Wait()
	}()

	return errChan
}

//go:nosplit
func noescape(p unsafe.Pointer) unsafe.Pointer {
	x := uintptr(p)
	return unsafe.Pointer(x ^ 0)
}

func processBatch[T any](ctx context.Context, wg *sync.WaitGroup, errChan chan error, signal Signal[T], list []Receiver[T], value T) {
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
