package redis

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"

	"github.com/Nigel2392/go-signals"
	"github.com/google/uuid"
)

type signal[T any] struct {
	name string
	pool *redisPool[T]
}

func (s *signal[T]) Name() string {
	return s.name
}

func (s *signal[T]) Send(ctx context.Context, v T) error {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(v)
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not encode %T to gob value", v,
		)
	}

	err = s.pool.client.Publish(ctx, s.name, base64.StdEncoding.EncodeToString(buf.Bytes())).Err()
	if err != nil {
		return signals.ErrSignal.WithCause(err).Wrapf(
			"could not publish %T", v,
		)
	}
	return nil
}

func (s *signal[T]) SendAsync(ctx context.Context, v T) chan error {
	var errChan chan error = make(chan error, 1)
	go func() {
		defer close(errChan)
		if err := s.pool.client.Publish(ctx, s.name, v).Err(); err != nil {
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
	for _, r := range recv {
		err := s.pool.disconnect(ctx, s, r)
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
