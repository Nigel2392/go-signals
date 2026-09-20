package pubsub

import (
	"github.com/Nigel2392/errors"
	"github.com/Nigel2392/go-signals"
)

const (
	CodeRetriesExceeded errors.GoCode = "RetriesExceeded"
	CodeContextError    errors.GoCode = "ContextError"
)

var (
	ErrPoolClosed      = signals.ErrPool.Wrap("pool is closed")
	ErrContext         = errors.New(CodeContextError, "error originated from context", ErrPoolClosed)
	ErrRetriesExceeded = errors.New(CodeRetriesExceeded, "retry count exceeded")
)
