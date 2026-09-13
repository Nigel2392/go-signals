//go:build arm64

#include "textflag.h"

TEXT ·Pause(SB),NOSPLIT,$0-0
	YIELD
	RET
