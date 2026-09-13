package pubsub

import (
	"context"
	"unsafe"
)

type contextKey struct {
	name    string
	topic   string
	pointer uintptr
}

func newKey[T any](usage string, topic string, onceObj *T) contextKey {
	var ptr uintptr
	if onceObj != nil {
		ptr = uintptr(unsafe.Pointer(onceObj))
	}

	return contextKey{
		name:    usage,
		topic:   topic,
		pointer: ptr,
	}
}

func ContextWith[T any](ctx context.Context, usage string, topic string, onceObj *T) context.Context {
	return context.WithValue(ctx, newKey(usage, topic, onceObj), struct{}{})
}

func ContextIs[T any](ctx context.Context, usage string, topic string, onceObj *T) bool {
	_, ok := ctx.Value(newKey(usage, topic, onceObj)).(struct{})
	return ok
}

var (
	poolContextKey        = newKey[struct{}]("pubsub.PoolFromContext", "", nil)
	messageContextKey     = newKey[struct{}]("pubsub.MessageFromContext", "", nil)
	messageMetaContextKey = newKey[struct{}]("pubsub.msgMetaFromContext", "", nil)
)

func MessageFromContext(ctx context.Context) *Message {
	var v, _ = ctx.Value(messageContextKey).(*Message)
	return v
}

func PoolFromContext[POOLTYPE any](ctx context.Context) *POOLTYPE {
	var v = ctx.Value(poolContextKey).(*POOLTYPE)
	return v
}

func ContextWithMessage(ctx context.Context, msg *Message) context.Context {
	return context.WithValue(ctx, messageContextKey, msg)
}

func contextWithPool(ctx context.Context, pool any) context.Context {
	return context.WithValue(ctx, poolContextKey, pool)
}

func ContextWithPool[POOLTYPE any](ctx context.Context, pool POOLTYPE) context.Context {
	return contextWithPool(ctx, pool)
}

func MsgMetaFromContext[POOLTYPE any](ctx context.Context, pool POOLTYPE) (meta map[string]any) {
	var v, _ = ctx.Value(messageMetaContextKey).(func(context.Context, POOLTYPE) map[string]any)
	if v != nil {
		meta = v(ctx, pool)
	}
	return meta
}

//	//go:nosplit
//	func noescape(p unsafe.Pointer) unsafe.Pointer {
//		x := uintptr(p)
//		return unsafe.Pointer(x ^ 0)
//	}
