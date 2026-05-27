package trustreceipt

import (
	"encoding/hex"
	"strconv"
)

type TokenCounts struct {
	Input  int64
	Output int64
	Cached int64
}

type PriceSnapshot struct {
	RateTableSnapshotID int64
	SnapshotVersion     string
	CurrencyCode        string
}

func ReceiptID(requestID string, seq int) string {
	if requestID == "" {
		return ""
	}
	return requestID + ":" + strconv.Itoa(seq)
}

func DisplayReceiptID(canonicalHash [32]byte) string {
	return "receipt_" + hex.EncodeToString(canonicalHash[:])[:32]
}
