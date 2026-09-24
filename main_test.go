package signals

import (
	"os"
	"testing"

	"github.com/Nigel2392/go-signals/internal/develop"
)

func TestMain(m *testing.M) {

	if develop.RaceEnabled {
		TOTAL_AMOUNT = 16000
	}

	exitCode := m.Run()

	os.Exit(exitCode)

}
