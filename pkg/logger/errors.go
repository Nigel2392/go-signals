package logger

import "github.com/Nigel2392/errors"

const (
	CodeUnknownLevel errors.GoCode = "UnknownLevel"
)

var (
	ErrUnknownLevel = errors.New(CodeUnknownLevel, "unknown log level")
)
