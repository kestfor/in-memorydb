package loadtest

import (
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
)

func RandomKey() string {
	b := make([]byte, 10)
	cryptorand.Read(b)
	return fmt.Sprintf("%x", b)
}

func RandomKeyWithRng(rng *rand.Rand) string {
	b := make([]byte, 10)
	rng.Read(b)
	return fmt.Sprintf("%x", b)
}

func RandomPayload(n int) []byte {
	b := make([]byte, n)
	cryptorand.Read(b)
	return b
}

func RandomPayloadWithRng(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}
