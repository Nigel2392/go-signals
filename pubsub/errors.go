package pubsub

import (
	"github.com/Nigel2392/errors"
	"github.com/Nigel2392/go-signals"
)

const (
	CodeRetriesExceeded errors.GoCode = "RetriesExceeded"
	CodeContextError    errors.GoCode = "ContextError"
	CodePoolClosed      errors.GoCode = "PoolClosed"
)

var (
	ErrPoolClosed      = errors.New(CodePoolClosed, "pool is closed", signals.ErrPool)
	ErrContext         = errors.New(CodeContextError, "error originated from context", ErrPoolClosed)
	ErrRetriesExceeded = errors.New(CodeRetriesExceeded, "retry count exceeded")
)
