package pubsub

import (
	"time"

	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type PoolOption[T any] func(p *Pool[T])

func PoolEncoder[T any](encoder encoder.Encoder) PoolOption[T] {
	return func(p *Pool[T]) {
		p.encoder = encoder
	}
}

func PoolOnError[T any](fn func(*Pool[T], error)) PoolOption[T] {
	return func(p *Pool[T]) {
		p.onErr = fn
	}
}

func PoolTickTime[T any](tickTime time.Duration) PoolOption[T] {
	return func(p *Pool[T]) {
		p.tickTime = tickTime
	}
}
