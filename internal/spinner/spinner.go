package spinner

import (
	"runtime"
)

// Spinner is used in tight loops to prevent the CPU
// from spinning out while still providing low latency.
//
// First 100 spins: [pause], [runtime.Gosched] every 16 spins
// 100 - 140 spins: [runtime.Gosched]
// 140 - 200 spins: [time.Sleep] for 1 microsecond
type Spinner uint32

func (s *Spinner) Spin() {
	i := *s

	switch {
	case i <= 100:
		// yield to scheduler every 16 spins
		// faster than modulo
		if i == 0 || (i&0x0F) == 0 {
			runtime.Gosched()
		} else {
			Pause()
		}
		*s++

	default:
		runtime.Gosched()
		*s++
	}
}

// Reset resets the spinner state.
func (s *Spinner) Reset() {
	*s = 0
}
