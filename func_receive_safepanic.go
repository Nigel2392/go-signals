//go:build safepanic
// +build safepanic

package signals

import (
	"context"
	"fmt"
)

func Receive[T any](ctx context.Context, s Signal[T], r Receiver[T], val T) (err error) {
	defer func() {
		// this shadows the returned error if any panic occurs during the execution of the receiver.
		if p := recover(); p != nil {
			if e, ok := p.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("panic recovered: %v", p)
			}
		}
	}()

	err = r.Receive(ctx, s, val)
	if err != nil {
		err = ErrReceiver.WithCause(err).Wrapf(
			"Receiver(%s)", r.ID(),
		)
	}
	return
}
