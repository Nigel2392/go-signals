//go:build !safepanic
// +build !safepanic

package signals

import (
	"context"
)

func Receive[T any](ctx context.Context, s Signal[T], r Receiver[T], val T) error {
	return r.Receive(ctx, s, val)
}
