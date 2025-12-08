package loadtest

import (
	"crypto/rand"
	"fmt"
)

func RandomKey() string {
	b := make([]byte, 10)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func RandomPayload(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
