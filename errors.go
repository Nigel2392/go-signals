package signals

import "strings"

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
