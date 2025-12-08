package gossip_buffer

import (
	"in-memorydb/pkg/structs"
	"in-memorydb/pkg/types"
	"sync"
)

type GossipBuffer struct {
	mu   sync.Mutex
	buff *structs.CircularBuffer[*types.Update]
}

func NewGossipBuffer(size int) *GossipBuffer {
	return &GossipBuffer{
		buff: structs.NewCircularBuffer[*types.Update](size, true),
	}
}

func (gb *GossipBuffer) Add(updates ...*types.Update) {
	gb.mu.Lock()
	for _, u := range updates {
		gb.buff.Add(u)
	}
	gb.mu.Unlock()
}

// AddAndDec adds updates with a positive TTL greater than 1 to the buffer after decrementing their TTL values by 1.
func (gb *GossipBuffer) AddAndDec(updates ...*types.Update) {
	gb.mu.Lock()
	for _, u := range updates {

		if u.TTL <= 1 {
			continue
		}

		u.TTL--
		gb.buff.Add(u)
	}
	gb.mu.Unlock()
}

// PeekN retrieves up to n elements from the buffer, removing them afterwards, and returns the retrieved elements.
func (gb *GossipBuffer) PeekN(n int) []*types.Update {
	gb.mu.Lock()
	n = min(n, gb.buff.Len())
	result := make([]*types.Update, n)
	for i, v := range gb.buff.All() {

		if i == n {
			break
		}

		result[i] = v
	}
	gb.buff.PopFirstN(n)
	gb.mu.Unlock()
	return result
}
