//go:build !batches
// +build !batches

package signals

import (
	"context"
	"math"
)

var DEFAULT_BATCH_SIZE = 0

const BATCHES = false

// Send a signal to all receivers asynchronously.
//
// Will error if there are no receivers.
//
// Returns an error, if any of the receivers return an error.
//
// This function is not fully tested, and might produce unexpected results.
//
// This function also will not check if there are any receivers.
//
// Returns a channel which will contain all errors from the receivers.
func (s *signal[T]) SendAsync(ctx context.Context, value T) chan error {
	recvs := s.getReceivers()

	// Check if there are any receivers.
	if len(recvs) == 0 {
		return nil
	}

	// Send the signal to each receiver.
	var errChan chan error = make(chan error, 1)
	go func() {
		defer close(errChan)
		var errs []error

		for _, receiver := range recvs {
			err := receiver.Receive(ctx, s, value)
			if err != nil {
				if errs == nil {
					errs = make([]error, 0, int(math.Max(float64(len(recvs))/20, float64(1))))
				}

				errs = append(errs, err)
			}
		}

		if len(errs) > 0 {
			errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
		}
	}()

	return errChan
}
