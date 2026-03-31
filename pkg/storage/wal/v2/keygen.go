package wal

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// 13 бит на строку (до 8192 строк), 51 бит на id
// нет коллизий пока не будет 8192 строк, ~2.25 квадриллиона id, для mvp более чем достаточно
const stringBits = 13

type keyMap = map[string]uint64

type KeyGen struct {
	mapping atomic.Pointer[keyMap]
	mu      sync.Mutex
	bits    int
	mask    uint64
}

func NewKeyGen() *KeyGen {
	kg := &KeyGen{
		bits: stringBits,
		mask: (1 << (64 - stringBits)) - 1,
	}
	kg.mapping.Store(new(make(keyMap)))
	return kg
}

func (kg *KeyGen) Key(s string, id uint64) uint64 {
	// fast path: lock-free read
	m := *kg.mapping.Load()
	idx, ok := m[s]
	if ok {
		return (idx << (64 - kg.bits)) | (id & kg.mask)
	}

	return kg.keySlow(s, id)
}

func (kg *KeyGen) keySlow(s string, id uint64) uint64 {
	kg.mu.Lock()
	defer kg.mu.Unlock()

	// double-check
	m := *kg.mapping.Load()
	if idx, ok := m[s]; ok {
		return (idx << (64 - kg.bits)) | (id & kg.mask)
	}

	// copy-on-write
	next := uint64(len(m))
	if next >= (1 << kg.bits) {
		slog.Info("keygen limit reached, collisions may occur")
	}

	newMap := make(keyMap, len(m)+1)
	for k, v := range m {
		newMap[k] = v
	}
	newMap[s] = next
	kg.mapping.Store(&newMap)

	return (next << (64 - kg.bits)) | (id & kg.mask)
}
