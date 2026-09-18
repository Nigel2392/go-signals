package subscriber

import (
	"github.com/Nigel2392/go-signals"
)

type SubSnapshot[VAL any, SIG signals.Signal[VAL]] struct {
	Topic string
	Sig   SIG
	Sub   *Subscriber[VAL]
}

func Snapshot[VAL any, SIG signals.Signal[VAL]](s map[string]*Subscriber[VAL], sigs map[string]SIG) []SubSnapshot[VAL, SIG] {
	snapShots := make([]SubSnapshot[VAL, SIG], 0, len(s))
	for k, sub := range s {
		sig, ok := sigs[k]
		if !ok {
			continue
		}

		snapShots = append(snapShots, SubSnapshot[VAL, SIG]{
			Topic: k,
			Sig:   sig,
			Sub:   sub,
		})
	}
	return snapShots
}
