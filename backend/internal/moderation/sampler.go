package moderation

import (
	"hash/fnv"
	"strconv"
)

func ShouldSample(requestID string, sampleRatePct int32) bool {
	if sampleRatePct <= 0 {
		return false
	}
	if sampleRatePct >= 100 {
		return true
	}
	key := requestID
	if key == "" {
		key = "empty-request-id"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int32(h.Sum32()%100) < sampleRatePct
}

func sampleKey(event ModerationEvent) string {
	if event.RequestID != "" {
		return event.RequestID
	}
	return strconv.FormatInt(event.TenantID, 10) + ":" + event.PayloadHash
}

func shouldSampleExternal(sampleRatePct int32, randomIntn func(int) int) bool {
	if sampleRatePct <= 0 {
		return false
	}
	if sampleRatePct >= 100 {
		return true
	}
	if randomIntn == nil {
		return false
	}
	draw := randomIntn(100)
	return draw >= 0 && draw < int(sampleRatePct)
}
