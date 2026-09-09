//go:build !batches
// +build !batches

package signals

import (
	"context"
	"math"
)

var DEFAULT_BATCH_SIZE = 0

const BATCHES = false

func asyncSend[T any](ctx context.Context, s Signal[T], recvs []Receiver[T], value T) chan error {
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
