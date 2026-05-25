package dispatcher

import "errors"

func canFallbackAfterPASRError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, ErrPASRPostMutationFail)
}
