package pubsub2

import (
	"fmt"
	"reflect"

	"github.com/Nigel2392/go-signals"
	"github.com/Nigel2392/go-signals/pubsub"
)

// The PubSubPool is the interface that [Pool] implements.
//
// It allows for easily implementing and using a subscribe- publish pattern.
//
// Current backends for this functionality are implemented in:
//
// * `github.com/Nigel2392/go-signals/pkg/memory`
// * `github.com/Nigel2392/go-signals/pkg/redis`
type PubSubPool pubsub.PubSubPool[any]

type PoolSignal interface {
	MsgType() reflect.Type
	signals.Signal[any]
}

type unwrapper[T any] interface {
	Unwrap() T
}

func unwrapRecvs[UNWRAPPED any](orig []signals.Receiver[any]) []signals.Receiver[UNWRAPPED] {
	var recvs = make([]signals.Receiver[UNWRAPPED], len(orig))
	for i, r := range orig {
		recvs[i] = TypedReceiver[UNWRAPPED](r)
	}
	return recvs
}

func TypedSignal[NEWT any](s signals.Signal[any]) signals.Signal[NEWT] {
	switch s := s.(type) {
	case unwrapper[signals.Signal[NEWT]]:
		return s.Unwrap()

	case unwrapper[*signal[NEWT]]:
		return s.Unwrap()
	default:
		panic(fmt.Sprintf("cannot unwrap %T into %s", s, reflect.TypeFor[signals.Signal[NEWT]]()))
	}
}

func TypedReceiver[NEWT any](s signals.Receiver[any]) signals.Receiver[NEWT] {
	switch s := s.(type) {
	case unwrapper[signals.Receiver[NEWT]]:
		return s.Unwrap()

	case unwrapper[*receiver[NEWT]]:
		return s.Unwrap()
	default:
		panic(fmt.Sprintf("cannot unwrap %T into %s", s, reflect.TypeFor[signals.Receiver[NEWT]]()))
	}
}
