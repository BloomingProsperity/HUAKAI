package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func ContentBindingDefaultEnabled() bool {
	return false
}

func OptInContentProof(key, nonce, content []byte) string {
	if len(key) == 0 || len(nonce) == 0 || len(content) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("huakai-opt-in-content-proof-v1"))
	mac.Write([]byte{0})
	mac.Write(nonce)
	mac.Write([]byte{0})
	mac.Write(content)
	return "proof:" + hex.EncodeToString(mac.Sum(nil))
}
