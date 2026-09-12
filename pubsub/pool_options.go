package pubsub

import (
	"context"
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type ConfigPool interface {
	WithEncoder(encoder.Encoder)
	WithTickDuration(time.Duration)
	WithInstanceID(uuid.UUID)
	WithAutoInitClient(bool)
}

type ConfigErrPool[POOLTYPE ConfigErrPool[POOLTYPE]] interface {
	WithOnError(func(context.Context, POOLTYPE, error))
}

type PoolOption func(p ConfigPool)

func PoolEncoder[T any](enc encoder.Encoder) PoolOption {
	return func(p ConfigPool) {
		p.WithEncoder(enc)
	}
}

func PoolOnError[POOLTYPE ConfigErrPool[POOLTYPE]](fn func(context.Context, POOLTYPE, error)) PoolOption {
	return func(p ConfigPool) {
		errSet := p.(POOLTYPE)
		errSet.WithOnError(fn)
	}
}

func PoolTickTime(tickTime time.Duration) PoolOption {
	return func(p ConfigPool) {
		p.WithTickDuration(tickTime)
	}
}

func PoolClientInit(b bool) PoolOption {
	return func(p ConfigPool) {
		p.WithAutoInitClient(b)
	}
}

func PoolWithUUID[T any](id uuid.UUID) PoolOption {
	return func(p ConfigPool) {
		p.WithInstanceID(id)
	}
}
