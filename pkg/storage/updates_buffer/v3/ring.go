package buffer

import (
	"sync/atomic"

	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
)

// TODO merge updates
type ringBuffer struct {
	buf  []atomic.Pointer[types.Update]
	cap  uint64
	head atomic.Uint64
}

func NewUpdatesBuffer(size int) *ringBuffer {
	r := &ringBuffer{
		buf: make([]atomic.Pointer[types.Update], size),
		cap: uint64(size),
	}
	return r
}

func (r *ringBuffer) Put(updates ...*types.Update) {
	for _, u := range updates {
		pos := r.head.Add(1)
		idx := pos % r.cap
		r.buf[idx].Store(u)
	}
}

func (r *ringBuffer) PeekN(n int) []*types.Update {
	out := make([]*types.Update, 0, n)

	head := r.head.Load()

	start := uint64(0)
	if head > r.cap {
		start = head - r.cap
	}

	for i := start; i < head && len(out) < n; i++ {
		idx := i % r.cap
		u := r.buf[idx].Load()
		if u != nil {
			out = append(out, u)
		}
	}

	return out
}

func (r *ringBuffer) Get(key string, nodeID string) ([]*types.Update, bool) {
	head := r.head.Load()

	start := uint64(0)
	if head > r.cap {
		start = head - r.cap
	}

	var out []*types.Update

	for i := start; i < head; i++ {
		u := r.buf[i%r.cap].Load()
		if u == nil {
			continue
		}

		if u.Key == key && u.NodeID == nodeID {
			out = append(out, u)
		}
	}

	if len(out) == 0 {
		return nil, false
	}

	return out, true
}

func (r *ringBuffer) GetCovering(nodeID string, rr structs.Range) []*types.Update {
	head := r.head.Load()

	start := uint64(0)
	if head > r.cap {
		start = head - r.cap
	}

	var out []*types.Update

	for i := start; i < head; i++ {
		u := r.buf[i%r.cap].Load()
		if u == nil {
			continue
		}

		if u.NodeID != nodeID {
			continue
		}

		if u.Range.Start <= rr.End && rr.Start <= u.Range.End {
			out = append(out, u)
		}
	}

	return out
}

func (r *ringBuffer) Remove(key, nodeID string) bool {
	head := r.head.Load()

	start := uint64(0)
	if head > r.cap {
		start = head - r.cap
	}

	removed := false

	for i := start; i < head; i++ {
		idx := i % r.cap
		u := r.buf[idx].Load()
		if u == nil {
			continue
		}

		if u.Key == key && u.NodeID == nodeID {
			r.buf[idx].Store(nil)
			removed = true
		}
	}

	return removed
}

func (r *ringBuffer) RemoveN(n int) int {
	head := r.head.Load()

	start := uint64(0)
	if head > r.cap {
		start = head - r.cap
	}

	removed := 0

	for i := start; i < head && removed < n; i++ {
		idx := i % r.cap
		u := r.buf[idx].Load()
		if u == nil {
			continue
		}

		r.buf[idx].Store(nil)
		removed++
	}

	return removed
}

func (r *ringBuffer) Len() int {
	head := r.head.Load()
	if head < r.cap {
		return int(head)
	}
	return int(r.cap)
}
