package pkg_test

import (
	"os"
	"testing"

	"github.com/Nigel2392/go-signals/internal/develop"
)

var (
	SEND_X_TIMES = 10000
	TOTAL_AMOUNT = 32000
)

func TestMain(m *testing.M) {

	if develop.RaceEnabled {
		SEND_X_TIMES = 5000
		TOTAL_AMOUNT = 16000
	}

	exitCode := m.Run()

	os.Exit(exitCode)

}
