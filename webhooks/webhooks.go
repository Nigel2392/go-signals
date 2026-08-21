package webhooks

import (
	"context"
	"net/http"

	"github.com/Nigel2392/go-signals"
)

type Webhook[T any] interface {
	// CallbackURL works both on producer and consumer side.
	CallbackURL(ctx context.Context) string

	// AddHeaders works both on producer and consumer side.
	AddHeaders(ctx context.Context, r *http.Request) error

	// Receives the signal and value from the signal.
	Receive(context.Context, signals.Signal[T], T) error
}
