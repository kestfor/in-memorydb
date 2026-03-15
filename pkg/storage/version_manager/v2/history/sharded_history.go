package history

import (
	"sort"
	"sync"

	"github.com/kestfor/in-memorydb/pkg/structs"
)

// NodeHistory хранит историю seq-id для одной ноды с per-node локом
type NodeHistory struct {
	mu     sync.RWMutex
	ranges []structs.Range // sorted by Start, non-overlapping
}

// ShardedHistory - thread-safe история с шардированием по nodeID
// Использует per-node локи вместо глобального для высокой производительности
type ShardedHistory struct {
	mu    sync.RWMutex
	nodes map[string]*NodeHistory
}

// NewShardedHistory создаёт новую шардированную историю
func NewShardedHistory() *ShardedHistory {
	return &ShardedHistory{
		nodes: make(map[string]*NodeHistory),
	}
}

// getOrCreate возвращает историю для ноды, создавая если нужно
// Использует double-check locking для минимизации блокировок
func (h *ShardedHistory) getOrCreate(nodeID string) *NodeHistory {
	// Fast path: read lock
	h.mu.RLock()
	nh, ok := h.nodes[nodeID]
	h.mu.RUnlock()
	if ok {
		return nh
	}

	// Slow path: write lock with double-check
	h.mu.Lock()
	defer h.mu.Unlock()

	if nh, ok = h.nodes[nodeID]; ok {
		return nh
	}

	nh = &NodeHistory{
		ranges: make([]structs.Range, 0, 16),
	}
	h.nodes[nodeID] = nh
	return nh
}

// get возвращает историю для ноды или nil
func (h *ShardedHistory) get(nodeID string) *NodeHistory {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.nodes[nodeID]
}

// Add добавляет одиночный seq в историю ноды
func (h *ShardedHistory) Add(nodeID string, seq uint64) {
	h.AddRange(nodeID, structs.Range{Start: seq, End: seq})
}

// AddRange добавляет range в историю ноды
func (h *ShardedHistory) AddRange(nodeID string, r structs.Range) {
	if r.Start > r.End {
		return
	}
	nh := h.getOrCreate(nodeID)

	nh.mu.Lock()
	defer nh.mu.Unlock()
	insertAndMerge(&nh.ranges, r)
}

// TryAddRange атомарно проверяет и добавляет range
// Возвращает true если range был добавлен (не существовал полностью ранее)
func (h *ShardedHistory) TryAddRange(nodeID string, r structs.Range) bool {
	if r.Start > r.End {
		return false
	}
	nh := h.getOrCreate(nodeID)

	nh.mu.Lock()
	defer nh.mu.Unlock()

	// Проверяем, полностью ли содержится range
	if containsRange(nh.ranges, r) {
		return false
	}

	insertAndMerge(&nh.ranges, r)
	return true
}

// HasRange проверяет, содержится ли range полностью в истории
func (h *ShardedHistory) HasRange(nodeID string, r structs.Range) bool {
	nh := h.get(nodeID)
	if nh == nil {
		return false
	}

	nh.mu.RLock()
	defer nh.mu.RUnlock()
	return containsRange(nh.ranges, r)
}

// Has проверяет наличие конкретного seq
func (h *ShardedHistory) Has(nodeID string, seq uint64) bool {
	nh := h.get(nodeID)
	if nh == nil {
		return false
	}

	nh.mu.RLock()
	defer nh.mu.RUnlock()
	return hasInRanges(nh.ranges, seq)
}

// ContiguousSeq возвращает contiguous seq для ноды
// Contiguous = максимальный T такой, что все seq от 1 до T присутствуют
func (h *ShardedHistory) ContiguousSeq(nodeID string) uint64 {
	nh := h.get(nodeID)
	if nh == nil {
		return 0
	}

	nh.mu.RLock()
	defer nh.mu.RUnlock()
	return calculateContiguous(nh.ranges)
}

// MaxSeq возвращает максимальный seq для ноды
func (h *ShardedHistory) MaxSeq(nodeID string) uint64 {
	nh := h.get(nodeID)
	if nh == nil {
		return 0
	}

	nh.mu.RLock()
	defer nh.mu.RUnlock()

	if len(nh.ranges) == 0 {
		return 0
	}
	return nh.ranges[len(nh.ranges)-1].End
}

// AllContiguousSeq возвращает contiguous seq для всех нод
func (h *ShardedHistory) AllContiguousSeq() map[string]uint64 {
	h.mu.RLock()
	nodes := make([]*NodeHistory, 0, len(h.nodes))
	nodeIDs := make([]string, 0, len(h.nodes))
	for id, nh := range h.nodes {
		nodes = append(nodes, nh)
		nodeIDs = append(nodeIDs, id)
	}
	h.mu.RUnlock()

	result := make(map[string]uint64, len(nodes))
	for i, nh := range nodes {
		nh.mu.RLock()
		result[nodeIDs[i]] = calculateContiguous(nh.ranges)
		nh.mu.RUnlock()
	}
	return result
}

// AllMaxSeq возвращает max seq для всех нод
func (h *ShardedHistory) AllMaxSeq() map[string]uint64 {
	h.mu.RLock()
	nodes := make([]*NodeHistory, 0, len(h.nodes))
	nodeIDs := make([]string, 0, len(h.nodes))
	for id, nh := range h.nodes {
		nodes = append(nodes, nh)
		nodeIDs = append(nodeIDs, id)
	}
	h.mu.RUnlock()

	result := make(map[string]uint64, len(nodes))
	for i, nh := range nodes {
		nh.mu.RLock()
		if len(nh.ranges) > 0 {
			result[nodeIDs[i]] = nh.ranges[len(nh.ranges)-1].End
		}
		nh.mu.RUnlock()
	}
	return result
}

// Diff возвращает ranges которых нет у remote для конкретной ноды
func (h *ShardedHistory) Diff(nodeID string, remoteLast uint64) []structs.Range {
	nh := h.get(nodeID)
	if nh == nil {
		return nil
	}

	nh.mu.RLock()
	defer nh.mu.RUnlock()

	var out []structs.Range
	for _, r := range nh.ranges {
		if r.End <= remoteLast {
			continue
		}
		start := max(r.Start, remoteLast+1)
		out = append(out, structs.Range{Start: start, End: r.End})
	}
	return out
}

// DiffAll вычисляет ranges которые есть у remote но нет локально
// remote[nodeID] = последний contiguous seq который remote имеет
func (h *ShardedHistory) DiffAll(remote map[string]uint64) map[string][]structs.Range {
	h.mu.RLock()
	localNodes := make(map[string]*NodeHistory, len(h.nodes))
	for id, nh := range h.nodes {
		localNodes[id] = nh
	}
	h.mu.RUnlock()

	result := make(map[string][]structs.Range)

	for nodeID, remoteLast := range remote {
		if remoteLast == 0 {
			continue
		}

		nh, ok := localNodes[nodeID]
		if !ok {
			// У нас нет этой ноды - запрашиваем всё
			result[nodeID] = []structs.Range{{Start: 1, End: remoteLast}}
			continue
		}

		nh.mu.RLock()
		missing := calculateMissing(nh.ranges, remoteLast)
		nh.mu.RUnlock()

		if len(missing) > 0 {
			result[nodeID] = missing
		}
	}

	return result
}

// Clear очищает историю для ноды
func (h *ShardedHistory) Clear(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.nodes, nodeID)
}

// ========== Helper functions ==========

// containsRange проверяет, содержится ли r полностью в ranges
func containsRange(ranges []structs.Range, r structs.Range) bool {
	for _, present := range ranges {
		if present.ContainsOther(r) {
			return true
		}
	}
	return false
}

// hasInRanges проверяет наличие seq в ranges
func hasInRanges(ranges []structs.Range, seq uint64) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].Start > seq })
	if i == 0 {
		return false
	}
	r := ranges[i-1]
	return r.Start <= seq && seq <= r.End
}

// calculateContiguous вычисляет максимальный T такой что все seq от 1 до T присутствуют
func calculateContiguous(ranges []structs.Range) uint64 {
	if len(ranges) == 0 {
		return 0
	}

	contig := uint64(0)
	for _, r := range ranges {
		if r.Start <= contig+1 {
			if r.End > contig {
				contig = r.End
			}
		} else {
			break
		}
	}
	return contig
}

// calculateMissing вычисляет ranges в [1, remoteLast] которых нет локально
func calculateMissing(ranges []structs.Range, remoteLast uint64) []structs.Range {
	if len(ranges) == 0 {
		return []structs.Range{{Start: 1, End: remoteLast}}
	}

	var missing []structs.Range
	cursor := uint64(1)

	for _, r := range ranges {
		if cursor > remoteLast {
			break
		}
		if r.End < cursor {
			continue
		}
		if r.Start > remoteLast {
			if cursor <= remoteLast {
				missing = append(missing, structs.Range{Start: cursor, End: remoteLast})
			}
			cursor = remoteLast + 1
			break
		}
		if r.Start > cursor {
			end := r.Start - 1
			if end > remoteLast {
				end = remoteLast
			}
			if cursor <= end {
				missing = append(missing, structs.Range{Start: cursor, End: end})
			}
		}
		if r.End+1 > cursor {
			cursor = r.End + 1
		}
	}

	if cursor <= remoteLast {
		missing = append(missing, structs.Range{Start: cursor, End: remoteLast})
	}

	return missing
}

// insertAndMerge вставляет range и сливает с соседними
func insertAndMerge(rs *[]structs.Range, r structs.Range) {
	if len(*rs) == 0 {
		*rs = []structs.Range{r}
		return
	}

	// Бинарный поиск позиции вставки
	n := len(*rs)
	idx := sort.Search(n, func(i int) bool {
		return (*rs)[i].Start >= r.Start
	})

	// Вставляем r на позицию idx
	*rs = append(*rs, structs.Range{})
	copy((*rs)[idx+1:], (*rs)[idx:])
	(*rs)[idx] = r
	n++

	// Находим левую границу зоны слияния
	startIdx := idx
	for startIdx > 0 && (*rs)[startIdx-1].End+1 >= (*rs)[startIdx].Start {
		startIdx--
	}

	// Находим правую границу зоны слияния
	endIdx := idx
	for endIdx+1 < n && (*rs)[endIdx].End+1 >= (*rs)[endIdx+1].Start {
		endIdx++
	}

	// Если нет зоны слияния
	if startIdx == endIdx {
		return
	}

	// Сливаем
	merged := (*rs)[startIdx]
	for i := startIdx + 1; i <= endIdx; i++ {
		merged.Start = min(merged.Start, (*rs)[i].Start)
		merged.End = max(merged.End, (*rs)[i].End)
	}

	copy((*rs)[startIdx+1:], (*rs)[endIdx+1:])
	(*rs)[startIdx] = merged
	*rs = (*rs)[:n-(endIdx-startIdx)]
}
