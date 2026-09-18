package pubsub2

import (
	"fmt"
	"reflect"

	"github.com/Nigel2392/go-signals"
)

type unwrapper[T any] interface {
	Unwrap() T
}

func unwrapRecvs[UNWRAPPED any](orig []signals.Receiver[any]) []signals.Receiver[UNWRAPPED] {
	var recvs = make([]signals.Receiver[UNWRAPPED], len(orig))
	for i, r := range orig {
		switch s := r.(type) {
		case *ifaceReceiver[UNWRAPPED]:
			recvs[i] = s.Receiver
		case *wrappedReceiver[UNWRAPPED]:
			recvs[i] = (*receiver[UNWRAPPED])(s)
		case unwrapper[signals.Receiver[UNWRAPPED]]:
			recvs[i] = s.Unwrap()
		case unwrapper[*receiver[UNWRAPPED]]:
			recvs[i] = s.Unwrap()
		default:
			panic(fmt.Sprintf("cannot unwrap %T into %s", s, reflect.TypeFor[signals.Receiver[UNWRAPPED]]()))
		}
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
	case *ifaceReceiver[NEWT]:
		return s.Receiver

	case *wrappedReceiver[NEWT]:
		return (*receiver[NEWT])(s)

	case unwrapper[signals.Receiver[NEWT]]:
		return s.Unwrap()

	case unwrapper[*receiver[NEWT]]:
		return s.Unwrap()

	default:
		panic(fmt.Sprintf("cannot unwrap %T into %s", s, reflect.TypeFor[signals.Receiver[NEWT]]()))
	}
}
