//go:build !batches && !isolate
// +build !batches,!isolate

package signals

import (
	"context"
	"iter"
)

const BATCHES = false

func AsyncReceiveIter[T any](ctx context.Context, s Signal[T], chSizeSuggestion int, receivers iter.Seq[Receiver[T]], val T) <-chan error {
	// Check if there are any receivers.
	if receivers == nil {
		return nil
	}

	// Send the signal to each receiver.
	var errChan chan error = make(chan error, 1)
	go func() {
		defer close(errChan)

		var errs []error
		for receiver := range receivers {
			// err := receive(ctx, s, receiver, val)
			err := Receive(ctx, s, receiver, val)
			if err != nil {
				errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
					"Receiver(%s)", receiver.ID(),
				))
			}
		}

		if len(errs) > 0 {
			errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
		}

	}()

	return errChan
}

func AsyncReceive[T any](ctx context.Context, s Signal[T], recvs []Receiver[T], value T) <-chan error {
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
			// err := receive(ctx, s, receiver, value)
			err := Receive(ctx, s, receiver, value)
			if err != nil {
				errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
					"Receiver(%s)", receiver.ID(),
				))
			}
		}

		if len(errs) > 0 {
			errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
		}
	}()

	return errChan
}

//	func AsyncTransmitIter[T any](ctx context.Context, s Transmitter[T], chSizeSuggestion int, receivers iter.Seq[Receiver[T]], val T) <-chan error {
//		// Check if there are any receivers.
//		if receivers == nil {
//			return nil
//		}
//
//		// Send the signal to each receiver.
//		var errChan chan error = make(chan error, 1)
//		go func() {
//			defer close(errChan)
//
//			var errs []error
//			for receiver := range receivers {
//				// err := receive(ctx, s, receiver, val)
//				err := s.Transmit(ctx, val, receiver)
//				if err != nil {
//					errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
//						"receiver %q:", receiver.ID(),
//					))
//				}
//			}
//
//			if len(errs) > 0 {
//				errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
//			}
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
//		// Send the signal to each receiver.
//		var errChan chan error = make(chan error, 1)
//		go func() {
//			defer close(errChan)
//
//			var errs []error
//			for _, receiver := range recvs {
//				// err := receive(ctx, s, receiver, value)
//				err := s.Transmit(ctx, value, receiver)
//				if err != nil {
//					errs = append(errs, ErrReceiver.WithCause(err).Wrapf(
//						"receiver %q:", receiver.ID(),
//					))
//				}
//			}
//
//			if len(errs) > 0 {
//				errChan <- Error{Val: "error(s) while executing receivers", Errors: errs}
//			}
//
//		}()
//
//		return errChan
//	}
