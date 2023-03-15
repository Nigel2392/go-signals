package signals

func SignalError(e error) (Error, bool) {
	switch e := e.(type) {
	case Error:
		return e, true
	default:
		return Error{Val: e.Error()}, false
	}
}

func e(val string, errors ...error) error {
	return Error{Val: val, Errors: errors}
}

// Error type for signals.
type Error struct {
	Val    string
	Errors []error
}

func (e Error) Error() string {
	return e.Val
}

func (e Error) Len() int {
	return len(e.Errors)
}
