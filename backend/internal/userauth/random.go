package userauth

import "crypto/rand"

func GenerateRandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, ErrInvalidInput
	}
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
