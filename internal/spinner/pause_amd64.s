//go:build amd64

#include "textflag.h"

TEXT ·Pause(SB),NOSPLIT,$0-0
	PAUSE
	RET
