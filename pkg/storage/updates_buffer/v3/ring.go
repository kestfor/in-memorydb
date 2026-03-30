package buffer

import (
	"log/slog"

	"github.com/hashicorp/golang-lru/v2"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
)

// TODO merge updates
type ringBuffer struct {
	cache *lru.Cache[string, types.Update]
}

func NewUpdatesBuffer(size int) *ringBuffer {
	cache, err := lru.New[string, types.Update](size)
	if err != nil {
		panic(err)
	}

	r := &ringBuffer{
		cache: cache,
	}
	return r
}

// если использовать чисто циклический буфер то в пике с 340к рпс вырастает до 387к
// размер буффера - 1000, при увеличении размера рпс будет падать в случае с ring, в отличие от lru
// при размере буффера в 10000 рпс lru - 381к, ring - 197k, lru явно выигрывает
func (r *ringBuffer) Put(updates ...types.Update) {
	for _, newUpd := range updates {
		cacheKey := newUpd.Key + ":" + newUpd.NodeID

		// TODO шиза с мержем и ссылками на апдейты
		upd, ok := r.cache.Get(cacheKey)
		if ok {

			err := upd.Merge(newUpd)
			if err != nil {
				slog.Error("failed to merge updates", "err", err)
				continue
			}
			r.cache.Add(cacheKey, upd)
		}

		r.cache.Add(newUpd.Key, newUpd)
	}
	//for _, u := range updates {
	//	found := false
	//	for ind := range r.buf {
	//		val := r.buf[ind].Load()
	//		if val != nil && val.Key == u.Key {
	//			_ = val.Merge(u)
	//			r.buf[ind].Store(val)
	//			found = true
	//		}
	//	}
	//	if !found {
	//		pos := r.head.Add(1)
	//		idx := pos % r.cap
	//		r.buf[idx].Store(u)
	//	}
	//}
}

func (r *ringBuffer) PeekN(n int) []types.Update {
	out := make([]types.Update, 0, n)

	for _, u := range r.cache.Values() {
		out = append(out, u)
		if len(out) == n {
			break
		}
	}

	//head := r.head.Load()
	//
	//start := uint64(0)
	//if head > r.cap {
	//	start = head - r.cap
	//}
	//
	//for i := start; i < head && len(out) < n; i++ {
	//	idx := i % r.cap
	//	u := r.buf[idx].Load()
	//	if u != nil {
	//		out = append(out, u)
	//	}
	//}

	return out
}

// TODO remove
func (r *ringBuffer) Get(key string, nodeID string) ([]types.Update, bool) {
	panic("deprecated, cannot be used")
	//head := r.head.Load()
	//
	//start := uint64(0)
	//if head > r.cap {
	//	start = head - r.cap
	//}
	//
	//var out []*types.Update
	//
	//for i := start; i < head; i++ {
	//	u := r.buf[i%r.cap].Load()
	//	if u == nil {
	//		continue
	//	}
	//
	//	if u.Key == key && u.NodeID == nodeID {
	//		out = append(out, u)
	//	}
	//}
	//
	//if len(out) == 0 {
	//	return nil, false
	//}
	//
	//return out, true
}

// TODO remove
func (r *ringBuffer) GetCovering(nodeID string, rr structs.Range) []types.Update {
	panic("deprecated, cannot be used")
	//head := r.head.Load()
	//
	//start := uint64(0)
	//if head > r.cap {
	//	start = head - r.cap
	//}
	//
	//var out []*types.Update
	//
	//for i := start; i < head; i++ {
	//	u := r.buf[i%r.cap].Load()
	//	if u == nil {
	//		continue
	//	}
	//
	//	if u.NodeID != nodeID {
	//		continue
	//	}
	//
	//	if u.Range.Start <= rr.End && rr.Start <= u.Range.End {
	//		out = append(out, u)
	//	}
	//}
	//
	//return out
}

// TODO remove
func (r *ringBuffer) Remove(key, nodeID string) bool {
	panic("deprecated cannot be used")
	//head := r.head.Load()
	//
	//start := uint64(0)
	//if head > r.cap {
	//	start = head - r.cap
	//}
	//
	//removed := false
	//
	//for i := start; i < head; i++ {
	//	idx := i % r.cap
	//	u := r.buf[idx].Load()
	//	if u == nil {
	//		continue
	//	}
	//
	//	if u.Key == key && u.NodeID == nodeID {
	//		r.buf[idx].Store(nil)
	//		removed = true
	//	}
	//}
	//
	//return removed
}

func (r *ringBuffer) RemoveN(n int) int {
	removed := 0
	for i := 0; i < r.cache.Len() && removed < n; i++ {
		_, _, _ = r.cache.RemoveOldest()
		removed++
	}

	//head := r.head.Load()
	//
	//start := uint64(0)
	//if head > r.cap {
	//	start = head - r.cap
	//}
	//
	//
	//for i := start; i < head && removed < n; i++ {
	//	idx := i % r.cap
	//	u := r.buf[idx].Load()
	//	if u == nil {
	//		continue
	//	}
	//
	//	r.buf[idx].Store(nil)
	//	removed++
	//}

	return removed
}

func (r *ringBuffer) Len() int {
	return r.cache.Len()
	//head := r.head.Load()
	//if head < r.cap {
	//	return int(head)
	//}
	//return int(r.cap)
}
