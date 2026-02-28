package utils

import "github.com/cespare/xxhash/v2"

func HashKey(key string) uint64 {
	return xxhash.Sum64String(key)
}
