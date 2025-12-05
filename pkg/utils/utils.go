package utils

import (
	"hash/fnv"
)

func HashKey(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}
