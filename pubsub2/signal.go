package pubsub2

import (
	"context"
	"reflect"

	"github.com/Nigel2392/go-signals"
	"github.com/google/uuid"
)

type wrappedSignal[T any] signal[T]

func (s *wrappedSignal[T]) Unwrap() *signal[T] {
	return (*signal[T])(s)
}

func (s *wrappedSignal[T]) Name() string {
	return (*signal[T])(s).Name()
}

func (s *wrappedSignal[T]) MsgType() reflect.Type {
	return (*signal[T])(s).MsgType()
}

func (s *wrappedSignal[T]) Clear(ctx context.Context) error {
	return (*signal[T])(s).Clear(ctx)
}

func (s *wrappedSignal[T]) Send(ctx context.Context, v any) error {
	return (*signal[T])(s).Send(ctx, v.(T))
}

func (s *wrappedSignal[T]) SendAsync(ctx context.Context, v any) chan error {
	return (*signal[T])(s).SendAsync(ctx, v.(T))
}

func (s *wrappedSignal[T]) Connect(ctx context.Context, recv ...signals.Receiver[any]) error {
	return (*signal[T])(s).Connect(ctx, unwrapRecvs[T](recv)...)
}

func (s *wrappedSignal[T]) Disconnect(ctx context.Context, recv ...signals.Receiver[any]) error {
	return (*signal[T])(s).Disconnect(ctx, unwrapRecvs[T](recv)...)
}

type wrappedSignalFunc func(context.Context, signals.Signal[any], any) error

func (w wrappedSignalFunc) wrapped[T any](ctx context.Context, s signals.Signal[T], t T) error {
	return w(ctx, (*wrappedSignal[T])(s.(*signal[T])), t)
}

func (s *wrappedSignal[T]) Listen(ctx context.Context, fn func(context.Context, signals.Signal[any], any) error) (signals.Receiver[any], error) {
	recv, err := (*signal[T])(s).Listen(ctx, wrappedSignalFunc(fn).wrapped[T])
	if err != nil {
		return nil, err
	}
	return (*wrappedReceiver[T])(recv.(*receiver[T])), nil
}

type signal[T any] struct {
	typ  reflect.Type
	name string
	pool *Pool
}

func (s *signal[T]) Name() string {
	return s.name
}

func (s *signal[T]) MsgType() reflect.Type {
	return s.typ
}

func (s *signal[T]) Send(ctx context.Context, v T) error {
	return s.pool.Send(ctx, s.name, v)
}

func (s *signal[T]) SendAsync(ctx context.Context, v T) chan error {
	var errChan chan error = make(chan error, 1)
	go func() {
		defer close(errChan)
		if err := s.Send(ctx, v); err != nil {
			errChan <- err
		}
	}()
	return errChan
}

func (s *signal[T]) Connect(ctx context.Context, recv ...signals.Receiver[T]) error {
	for _, r := range recv {
		err := r.Bind(ctx, s)
		if err != nil {
			return signals.ErrReceiver.WithCause(err).Wrapf(
				"receiver %q", r.ID(),
			)
		}

		err = s.pool.connect(ctx, s.name, r)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *signal[T]) Clear(ctx context.Context) error {
	return s.pool.clear(ctx, s.name)
}

func (s *signal[T]) Disconnect(ctx context.Context, recv ...signals.Receiver[T]) error {
	if len(recv) == 0 {
		return s.pool.clear(ctx, s.name)
	}

	for _, r := range recv {
		err := s.pool.disconnect(ctx, s, r.(*receiver[T]))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *signal[T]) Listen(ctx context.Context, fn func(context.Context, signals.Signal[T], T) error) (signals.Receiver[T], error) {
	recv := &receiver[T]{id: uuid.New().String(), cb: fn}
	return recv, s.Connect(ctx, recv)
}
