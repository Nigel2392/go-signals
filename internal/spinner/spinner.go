package spinner

import (
	"runtime"
)

// Spinner is used in tight loops to prevent the CPU
// from spinning out while still providing low latency.
type Spinner uint32

func (s *Spinner) Spin() {
	if (*s) == 0 || ((*s)&0x0F) == 0 {
		runtime.Gosched()
	} else {
		Pause()
	}

	*s++
}

// Reset resets the spinner state.
func (s *Spinner) Reset() {
	*s = 0
}
