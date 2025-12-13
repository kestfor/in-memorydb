package buffer

import (
	"container/list"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
	"log/slog"
	"sort"
	"sync"
)

// Описание
// Buffer нужен для того, чтобы складывать формирующиеся обновления при получении данных от клиентов/других нод
// Буфер построен по принципу LRU, с фиксированно длиной, новые обновления вытесняют старые
// единицу Update можно мержить между собой если они приходят из одной ноды для экономии пространства
// между разными нодами update пока было принято не мержить, в будущем подход может измениться
// когда по одному ключу и одной ноде добавляется update размер буфера не увеличивается так как обновления сливаются

type Buffer struct {
	rwlock  sync.RWMutex
	items   *list.List
	lookup  map[string]map[string][]*list.Element // key -> nodeId -> []*list.Element
	maxSize int
}

func NewBuffer(maxSize int) *Buffer {
	return &Buffer{
		maxSize: maxSize,
		lookup:  make(map[string]map[string][]*list.Element, maxSize),
		items:   list.New(),
	}
}

// Put добавляет или обновляет элемент в буфере.
// Если элемент существует — обновляет value и перемещает в front.
// Если новый элемент и размер превысил maxSize — удаляет старейший.
func (b *Buffer) Put(updates ...*types.Update) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	// Group incoming updates by key-nodeID
	type keyNode struct {
		key    string
		nodeID string
	}
	grouped := make(map[keyNode][]*types.Update)
	for _, u := range updates {
		kn := keyNode{u.Key, u.NodeID}
		grouped[kn] = append(grouped[kn], u)
	}

	for kn, incoming := range grouped {
		// Ensure map for key exists
		nodes, ok := b.lookup[kn.key]
		if !ok {
			nodes = make(map[string][]*list.Element)
			b.lookup[kn.key] = nodes
		}

		// Get existing elements
		existingEls := nodes[kn.nodeID]

		// Collect all existing updates
		var all []*types.Update
		for _, el := range existingEls {
			all = append(all, el.Value.(*types.Update))
		}

		// Append incoming
		all = append(all, incoming...)

		// Collapse all
		collapsed := b.collapseUpdates(all)

		// Remove old elements
		for _, el := range existingEls {
			b.items.Remove(el)
		}

		// Add new collapsed updates to front, in reverse to preserve ascending range order
		newEls := make([]*list.Element, 0, len(collapsed))
		for i := len(collapsed) - 1; i >= 0; i-- {
			el := b.items.PushFront(collapsed[i])
			newEls = append(newEls, el)
		}

		// Update lookup
		nodes[kn.nodeID] = newEls
	}

	// Evict if over maxSize
	diff := b.items.Len() - b.maxSize
	if diff > 0 {
		b.removeNLocked(diff)
	}
}

func (b *Buffer) collapseUpdates(upds []*types.Update) []*types.Update {
	if len(upds) <= 1 {
		return upds
	}

	// Sort by range start
	sort.Slice(upds, func(i, j int) bool {
		return upds[i].Range.Start < upds[j].Range.Start
	})

	merged := make([]*types.Update, 0, len(upds))
	current := upds[0]

	for i := 1; i < len(upds); i++ {
		next := upds[i]
		if current.Range.End+1 >= next.Range.Start {
			// Merge next into current
			if err := current.Merge(next); err != nil {
				slog.Error("Error while merging updates", "key", current.Key, "nodeId", current.NodeID, "err", err)
			}

		} else {
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)

	return merged
}

// Get возвращает элемент и помечает его как недавно использованный.
// Возвращается (value, true) если найдено, иначе (nil, false).
func (b *Buffer) Get(key, nodeId string) ([]*types.Update, bool) {
	b.rwlock.Lock() // мы будем перемещать элемент в front, поэтому нужен write lock
	defer b.rwlock.Unlock()

	nodes, ok := b.lookup[key]
	if !ok {
		return nil, false
	}
	els, found := nodes[nodeId]
	if !found {
		return nil, false
	}

	// Sort els by range start for consistent order
	sort.Slice(els, func(i, j int) bool {
		return els[i].Value.(*types.Update).Range.Start < els[j].Value.(*types.Update).Range.Start
	})

	// Move to front in reverse to preserve order
	for i := len(els) - 1; i >= 0; i-- {
		b.items.MoveToFront(els[i])
	}

	// Collect updates in sorted order
	updates := make([]*types.Update, len(els))
	for i, el := range els {
		updates[i] = el.Value.(*types.Update)
	}

	return updates, true
}

// Remove удаляет конкретный элемент. Возвращает true если элемент был удалён.
func (b *Buffer) Remove(key, nodeId string) bool {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	nodes, ok := b.lookup[key]
	if !ok {
		return false
	}
	els, found := nodes[nodeId]
	if !found {
		return false
	}

	// Remove all elements
	for _, el := range els {
		b.items.Remove(el)
	}

	// Remove from lookup
	delete(nodes, nodeId)
	if len(nodes) == 0 {
		delete(b.lookup, key)
	}
	return true
}

func (b *Buffer) RemoveN(n int) (removedN int) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()
	return b.removeNLocked(n)
}

func (b *Buffer) removeNLocked(n int) (removedN int) {
	for i := 0; i < n; i++ {
		if b.items.Len() == 0 {
			break
		}
		b.removeOldestLocked()
		removedN++
	}
	return removedN
}

// Len возвращает текущий размер буфера (кол-во элементов).
func (b *Buffer) Len() int {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	return b.items.Len()
}

// PeekOldest возвращает самый старый элемент (LRU) без удаления.
func (b *Buffer) PeekOldest() (*types.Update, bool) {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()

	el := b.items.Back()
	if el == nil {
		return nil, false
	}
	if bi, ok := el.Value.(*types.Update); ok {
		return bi, true
	}
	return nil, false
}

// PeekN peeks first n updates and returns it as slice
// time complexity O(n)
func (b *Buffer) PeekN(n int) []*types.Update {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	if n > b.items.Len() {
		n = b.items.Len()
	}
	res := make([]*types.Update, 0, n)
	el := b.items.Front()
	for i := 0; i < n; i++ {
		if bi, ok := el.Value.(*types.Update); ok {
			res = append(res, bi)
		}
		el = el.Next()
	}
	return res
}

// GetCovering returns a list of updates whose ranges overlap with the specified range.
// time - O(n)
func (b *Buffer) GetCovering(nodeID string, r structs.Range) []*types.Update {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	var result []*types.Update

	for el := b.items.Front(); el != nil; el = el.Next() {
		if bi, ok := el.Value.(*types.Update); ok {
			if nodeID != bi.NodeID {
				continue
			}

			// full cover
			if bi.Range.ContainsOther(r) {
				result = append(result, bi)
				break
			}

			if r.ContainsOther(bi.Range) {
				result = append(result, bi)
				continue
			}

			// end covering
			if r.Start <= bi.Range.Start && bi.Range.Start <= r.End {
				r.Start = bi.Range.Start
				result = append(result, bi)
			}

			// start covering
			if r.Start <= bi.Range.End && bi.Range.End <= r.End {
				r.Start = bi.Range.End
				result = append(result, bi)
			}
		}
	}
	return result
}

// PopOldest удаляет и возвращает самый старый элемент (LRU).
func (b *Buffer) PopOldest() (*types.Update, bool) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()
	val := b.removeOldestLocked()
	if val == nil {
		return nil, false
	} else {
		return val, true
	}
}

// removeOldestLocked удаляет самый старый элемент. Предполагается, что вызывается
// с захваченным Lock() (locked).
func (b *Buffer) removeOldestLocked() *types.Update {
	el := b.items.Back()
	if el == nil {
		return nil
	}
	bi := el.Value.(*types.Update)
	b.items.Remove(el)

	// Remove from lookup
	if nodes, ok := b.lookup[bi.Key]; ok {
		els := nodes[bi.NodeID]
		for i, e := range els {
			if e == el {
				// Remove from slice
				els[i] = els[len(els)-1]
				els = els[:len(els)-1]
				break
			}
		}
		nodes[bi.NodeID] = els
		if len(els) == 0 {
			delete(nodes, bi.NodeID)
		}
		if len(nodes) == 0 {
			delete(b.lookup, bi.Key)
		}
	}
	return bi
}
