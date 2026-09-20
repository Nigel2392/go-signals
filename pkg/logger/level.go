package logger

import (
	"context"
)

type Loglevel int

const (
	DEBUG Loglevel = iota
	INFO
	WARN
	ERROR
)

var maxLevel Loglevel

func init() {
	RegisterLevel(DEBUG, levelString("DEBUG").Stringer)
	RegisterLevel(INFO, levelString("INFO").Stringer)
	RegisterLevel(WARN, levelString("WARNING").Stringer)
	RegisterLevel(ERROR, levelString("ERROR").Stringer)
}

type levelString string

func (l levelString) Stringer(ctx context.Context) (string, error) {
	return (string)(l), nil
}

// not safe to write concurrently
var levelReg = make(map[Loglevel]func(context.Context) (string, error), 4)

func RegisterLevel(l Loglevel, s func(context.Context) (string, error)) (overwrite bool) {
	if l > maxLevel {
		maxLevel = l
	}

	_, ok := levelReg[l]
	levelReg[l] = s
	return ok
}

func SkipLog(ctx context.Context, loggerLevel, messageLevel Loglevel) bool {
	return levelFromContext(ctx, loggerLevel) > messageLevel
}

func loglevelString(ctx context.Context, l Loglevel) (string, error) {
	fn, ok := levelReg[l]
	if !ok {
		return "", ErrUnknownLevel.Wrapf("loglevel %d not found in registry", l)
	}

	return fn(ctx)
}

type logLevelKey struct{}

func ContextWithLoglevel(ctx context.Context, l Loglevel) context.Context {
	return context.WithValue(ctx, logLevelKey{}, l)
}

func levelFromContext(ctx context.Context, fallback Loglevel) Loglevel {
	v, ok := ctx.Value(logLevelKey{}).(Loglevel)
	if !ok {
		return fallback
	}
	return v
}
