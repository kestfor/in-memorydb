package updates_buffer

import (
	"container/list"
	"in-memorydb/pkg/storage"
	"log/slog"
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
	lookup  map[string]map[string]*list.Element // key -> nodeId -> update mapping
	maxSize int
}

func NewBuffer(maxSize int) *Buffer {
	return &Buffer{
		maxSize: maxSize,
		lookup:  make(map[string]map[string]*list.Element, maxSize),
		items:   list.New(),
	}
}

// Put добавляет или обновляет элемент в буфере.
// Если элемент существует — обновляет value и перемещает в front.
// Если новый элемент и размер превысил maxSize — удаляет старейший.
func (b *Buffer) Put(updates ...*storage.Update) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	for _, update := range updates {

		// ensure map for key exists
		nodes, ok := b.lookup[update.Key]
		if !ok {
			nodes = make(map[string]*list.Element)
			b.lookup[update.Key] = nodes
		}

		// if exists -> replace value and move to front
		if el, found := nodes[update.NodeID]; found {
			if old, ok := el.Value.(*storage.Update); ok {
				err := old.Merge(update)

				if err != nil {
					slog.Error("Error while adding update to buffer", "key", update.Key, "nodeId", update.NodeID, "err", err, "update", update)
				}

				b.items.MoveToFront(el)
				return
			}
		}

		// otherwise push new
		el := b.items.PushFront(update)
		b.lookup[update.Key][update.NodeID] = el
	}
	diff := b.items.Len() - b.maxSize
	if diff > 0 {
		b.removeNLocked(diff)
	}
}

// Get возвращает элемент и помечает его как недавно использованный.
// Возвращается (value, true) если найдено, иначе (nil, false).
func (b *Buffer) Get(key, nodeId string) (*storage.Update, bool) {
	b.rwlock.Lock() // мы будем перемещать элемент в front, поэтому нужен write lock
	defer b.rwlock.Unlock()

	if nodes, ok := b.lookup[key]; ok {
		if el, found := nodes[nodeId]; found {
			if bi, ok := el.Value.(*storage.Update); ok {
				b.items.MoveToFront(el)
				return bi, true
			}
		}
	}

	return nil, false
}

// Remove удаляет конкретный элемент. Возвращает true если элемент был удалён.
func (b *Buffer) Remove(key, nodeId string) bool {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	if nodes, ok := b.lookup[key]; ok {
		if el, found := nodes[nodeId]; found {
			// remove from list
			b.items.Remove(el)
			// remove from map
			delete(nodes, nodeId)
			if len(nodes) == 0 {
				delete(b.lookup, key)
			}
			return true
		}
	}
	return false
}

func (b *Buffer) RemoveN(n int) (removedN int) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()
	return b.removeNLocked(n)
}

func (b *Buffer) removeNLocked(n int) (removedN int) {
	n = min(n, b.items.Len())
	for i := 0; i < n; i++ {
		b.removeOldestLocked()
	}
	return n
}

// Len возвращает текущий размер буфера (кол-во элементов).
func (b *Buffer) Len() int {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	return b.items.Len()
}

// PeekOldest возвращает самый старый элемент (LRU) без удаления.
func (b *Buffer) PeekOldest() (*storage.Update, bool) {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()

	el := b.items.Back()
	if el == nil {
		return nil, false
	}
	if bi, ok := el.Value.(*storage.Update); ok {
		return bi, true
	}
	return nil, false
}

// PeekN peeks first n updates and returns it as slice
// time complexity O(n)
func (b *Buffer) PeekN(n int) []*storage.Update {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	n = min(b.items.Len(), n)
	res := make([]*storage.Update, 0, n)
	for el := b.items.Front(); el != nil; el = el.Next() {
		if bi, ok := el.Value.(*storage.Update); ok {
			res = append(res, bi)
		}
	}
	return res
}

// PopOldest удаляет и возвращает самый старый элемент (LRU).
func (b *Buffer) PopOldest() (*storage.Update, bool) {
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
func (b *Buffer) removeOldestLocked() *storage.Update {
	el := b.items.Back()
	if el == nil {
		return nil
	}
	if bi, ok := el.Value.(*storage.Update); ok {
		b.items.Remove(el)
		if nodes, ok := b.lookup[bi.Key]; ok {
			delete(nodes, bi.NodeID)
			if len(nodes) == 0 {
				delete(b.lookup, bi.Key)
			}
		}
		return bi
	}
	return nil
}
