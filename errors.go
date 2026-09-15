package signals

import (
	"strings"

	"github.com/Nigel2392/errors"
)

const (
	CodeNotSupported  errors.GoCode = "NotSupported"
	CodeSignalError   errors.GoCode = "SignalError"
	CodeReceiverError errors.GoCode = "ReceiverError"
	CodePoolError     errors.GoCode = "PoolError"
)

var (
	ErrPool        = new(errors.New(CodePoolError, "pool error"))
	ErrSignal      = new(errors.New(CodeSignalError, "signal error"))
	ErrReceiver    = new(errors.New(CodeReceiverError, "receiver error"))
	ErrUnsupported = new(errors.New(CodeNotSupported, "operation not supported"))
)

func SignalError(e error) (Error, bool) {
	switch e := e.(type) {
	case Error:
		return e, true
	default:
		var t = new(Error)
		if errors.As(e, t) {
			return *t, true
		}

		return Error{Val: e.Error()}, false
	}
}

func Err(val string, errors ...error) error {
	return Error{Val: val, Errors: errors}
}

// Error type for signals.
type Error struct {
	Val    string
	Errors []error
}

func (e Error) Error() string {
	var b = new(strings.Builder)
	b.WriteString(e.Val)
	b.WriteString(" (")
	for i, err := range e.Errors {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(err.Error())
	}
	b.WriteString(")")
	return b.String()
}

func (e Error) Len() int {
	return len(e.Errors)
}

func (e Error) Unwrap() []error {
	return e.Errors
}
