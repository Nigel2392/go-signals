package signals

import (
	"github.com/Nigel2392/errors"
)

const (
	CodeNotSupported  errors.GoCode = "NotSupported"
	CodeSignalError   errors.GoCode = "SignalError"
	CodeReceiverError errors.GoCode = "ReceiverError"
	CodePoolError     errors.GoCode = "PoolError"
)

var (
	ErrPool        = errors.New(CodePoolError, "pool error")
	ErrSignal      = errors.New(CodeSignalError, "signal error")
	ErrReceiver    = errors.New(CodeReceiverError, "receiver error")
	ErrUnsupported = errors.New(CodeNotSupported, "operation not supported")

	ErrSignalNotFound errors.AbstractError[errors.Error] = ErrSignal.Wrap("signal not found")
	ErrNoReceivers    errors.AbstractError[errors.Error] = ErrSignal.Wrap("did not provide any receivers to disconnect")
)

func SignalError(e error) (errors.Error, bool) {
	switch e := e.(type) {
	case errors.Error:
		return e, true

	default:
		var t errors.Error
		if errors.As(e, &t) {
			return t, true
		}

		return errors.Error{Code: errors.CodeUnknown, Reason: e}, false
	}
}

type receiverIdType interface {
	// Return the unique ID of the receiver.
	ID() string
}

func ReceiverError[RECEIVER receiverIdType](recv RECEIVER, cause error) error {
	return errors.Error{
		Code:    CodeReceiverError,
		Reason:  cause,
		Message: recv.ID(),
	}
}

func PoolError(cause error, where string) error {
	return errors.Error{
		Code:    CodePoolError,
		Reason:  cause,
		Message: where,
	}
}
