package trustreceipt

import (
	"encoding/base64"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

var ErrSignerNil = errors.New("trustreceipt: signer is nil")

func SignReceipt(signer *sign.Signer, r TrustReceiptV1) (sigB64 string, fingerprint string, err error) {
	if signer == nil {
		return "", "", ErrSignerNil
	}
	canonical, err := Canonical(r)
	if err != nil {
		return "", "", err
	}
	signature := signer.Sign(canonical)
	return base64.StdEncoding.EncodeToString(signature), signer.Fingerprint(), nil
}

