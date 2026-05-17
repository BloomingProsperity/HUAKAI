package privacy

import (
	"crypto/subtle"
	"runtime"
)

func Zeroize(buf []byte) {
	if len(buf) == 0 {
		return
	}
	zeros := make([]byte, len(buf))
	subtle.ConstantTimeCopy(1, buf, zeros)
	for i := range buf {
		buf[i] = 0
	}
	runtime.KeepAlive(buf)
}
