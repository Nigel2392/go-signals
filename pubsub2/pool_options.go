package pubsub2

import (
	"time"
	"uuid"

	"github.com/Nigel2392/go-signals/pubsub/encoder"
)

type PoolOption func(p *Pool)

func PoolEncoder(encoder encoder.Encoder) PoolOption {
	return func(p *Pool) {
		p.encoder = encoder
	}
}

func PoolOnError(fn func(*Pool, error)) PoolOption {
	return func(p *Pool) {
		p.onErr = fn
	}
}

func PoolTickTime(tickTime time.Duration) PoolOption {
	return func(p *Pool) {
		p.tickTime = tickTime
	}
}

func PoolWithUUID(id uuid.UUID) PoolOption {
	return func(p *Pool) {
		p.inst = id
	}
}
