package buffer

import (
	"container/list"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
	"log/slog"
	"sort"
	"sync"
)

// Buffer implements an LRU buffer optimized for CRDT delta updates in P2P gossip networks.
// It maintains three indexes:
// 1. LRU linked list for eviction order (newest at front)
// 2. Key-NodeID lookup map for fast merge operations
// 3. Per-NodeID range indexes for O(log n) range queries
//
// Memory: O(n) where n = maxSize
// GetCovering: O(log m + k) where m = updates for nodeID, k = query results
type Buffer struct {
	rwlock    sync.RWMutex
	items     *list.List                            // LRU chain: newest (front) -> oldest (back)
	lookup    map[string]map[string][]*list.Element // key -> nodeID -> []*list.Element
	nodeIDIdx map[string]*nodeRangeIndex            // nodeID -> sorted range index
	maxSize   int
}

// nodeRangeIndex maintains a sorted slice of updates for a single nodeID.
// Sorted by Range.Start to enable binary search in GetCovering queries.
// This structure trades O(m log m) insertion cost for O(log m) search cost.
type nodeRangeIndex struct {
	updates []*types.Update // invariant: sorted by Range.Start ascending
}

func NewBuffer(maxSize int) *Buffer {
	return &Buffer{
		maxSize:   maxSize,
		lookup:    make(map[string]map[string][]*list.Element, maxSize),
		nodeIDIdx: make(map[string]*nodeRangeIndex),
		items:     list.New(),
	}
}

// Put adds or merges updates into the buffer.
// Updates from the same (key, nodeID) pair are merged to save memory by collapsing
// consecutive updates with continuous seq_num ranges.
// When buffer exceeds maxSize, oldest elements (by LRU) are evicted.
//
// Time complexity: O(m log m + k log k) where:
//
//	m = updates per (key, nodeID) pair
//	k = total updates for nodeID across all keys
func (b *Buffer) Put(updates ...*types.Update) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	// Group updates by (key, nodeID) to merge them separately
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
		// Ensure lookup maps exist
		nodes, ok := b.lookup[kn.key]
		if !ok {
			nodes = make(map[string][]*list.Element)
			b.lookup[kn.key] = nodes
		}

		// Get existing elements for this (key, nodeID) pair
		existingEls := nodes[kn.nodeID]

		// Collect all existing updates
		var all []*types.Update
		for _, el := range existingEls {
			all = append(all, el.Value.(*types.Update))
		}

		// Merge with incoming updates
		all = append(all, incoming...)

		// Collapse consecutive updates with continuous seq_num ranges
		collapsed := b.collapseUpdates(all)

		// Remove old elements from LRU list
		for _, el := range existingEls {
			b.items.Remove(el)
		}

		// Remove old updates from per-nodeID range index
		b.removeFromNodeIDIndex(kn.nodeID, existingEls)

		// Add new collapsed updates to front of LRU list (most recently used)
		// Process in reverse to maintain ascending Range.Start order
		newEls := make([]*list.Element, 0, len(collapsed))
		for i := len(collapsed) - 1; i >= 0; i-- {
			el := b.items.PushFront(collapsed[i])
			newEls = append(newEls, el)
		}

		// Update lookup with new elements
		nodes[kn.nodeID] = newEls

		// Add to per-nodeID range index (maintains sorted order)
		b.addToNodeIDIndex(kn.nodeID, collapsed)
	}

	// Evict oldest elements if buffer exceeds maxSize
	diff := b.items.Len() - b.maxSize
	if diff > 0 {
		b.removeNLocked(diff)
	}
}

// collapseUpdates merges consecutive updates with continuous seq_num ranges.
// Gaps in seq_num ranges are preserved as separate update elements.
// This heuristic reduces memory usage while preventing loss of information needed for sync.
//
// Time complexity: O(m log m) where m = len(updates) due to sorting
func (b *Buffer) collapseUpdates(upds []*types.Update) []*types.Update {
	if len(upds) <= 1 {
		return upds
	}

	// Sort by Range.Start (seq_num start) to identify consecutive ranges
	sort.Slice(upds, func(i, j int) bool {
		return upds[i].Range.Start < upds[j].Range.Start
	})

	merged := make([]*types.Update, 0, len(upds))
	current := upds[0]

	for i := 1; i < len(upds); i++ {
		next := upds[i]
		// Check if ranges are consecutive and can be merged
		// Merge if: current.End + 1 >= next.Start (no gap or adjacent)
		if current.Range.End+1 >= next.Range.Start {
			if err := current.Merge(next); err != nil {
				slog.Error("error merging CRDT updates",
					"key", current.Key,
					"nodeID", current.NodeID,
					"current_range", current.Range,
					"next_range", next.Range,
					"err", err)
			}
		} else {
			// Gap detected: cannot merge these ranges
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)

	return merged
}

// Get returns all updates for (key, nodeID) and marks them as recently used in LRU.
// Returns (updates, true) if found, (nil, false) otherwise.
//
// Time complexity: O(k log k) where k = number of updates for this (key, nodeID) pair
func (b *Buffer) Get(key, nodeID string) ([]*types.Update, bool) {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	nodes, ok := b.lookup[key]
	if !ok {
		return nil, false
	}
	els, found := nodes[nodeID]
	if !found {
		return nil, false
	}

	// Sort elements by Range.Start for consistent ascending order
	sort.Slice(els, func(i, j int) bool {
		return els[i].Value.(*types.Update).Range.Start < els[j].Value.(*types.Update).Range.Start
	})

	// Move all elements to front in reverse order to maintain order and mark as recently used
	for i := len(els) - 1; i >= 0; i-- {
		b.items.MoveToFront(els[i])
	}

	// Collect updates from list elements
	updates := make([]*types.Update, len(els))
	for i, el := range els {
		updates[i] = el.Value.(*types.Update)
	}

	return updates, true
}

// Remove deletes all updates for a specific (key, nodeID) pair.
// Returns true if removal occurred, false if pair didn't exist.
//
// Time complexity: O(k + log m) where k = updates for pair, m = updates for nodeID
func (b *Buffer) Remove(key, nodeID string) bool {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()

	nodes, ok := b.lookup[key]
	if !ok {
		return false
	}
	els, found := nodes[nodeID]
	if !found {
		return false
	}

	// Remove all elements from LRU list
	for _, el := range els {
		b.items.Remove(el)
	}

	// Remove from per-nodeID range index
	b.removeFromNodeIDIndex(nodeID, els)

	// Remove from lookup
	delete(nodes, nodeID)
	if len(nodes) == 0 {
		delete(b.lookup, key)
	}

	return true
}

// RemoveN removes the n oldest elements according to LRU policy.
// Returns the actual number of elements removed.
//
// Time complexity: O(n * (log m + d)) where m = nodeID index size, d = lookup depth
func (b *Buffer) RemoveN(n int) int {
	b.rwlock.Lock()
	defer b.rwlock.Unlock()
	return b.removeNLocked(n)
}

func (b *Buffer) removeNLocked(n int) int {
	removed := 0
	for i := 0; i < n && b.items.Len() > 0; i++ {
		b.removeOldestLocked()
		removed++
	}
	return removed
}

// removeOldestLocked removes the oldest element (LRU) and updates all indexes.
// Must be called with write lock held.
//
// Time complexity: O(log m + d) where m = nodeID index size, d = lookup depth
func (b *Buffer) removeOldestLocked() *types.Update {
	el := b.items.Back()
	if el == nil {
		return nil
	}

	upd := el.Value.(*types.Update)
	b.items.Remove(el)

	// Remove from per-nodeID range index
	b.removeFromNodeIDIndexLocked(upd.NodeID, upd)

	// Remove from lookup
	if nodes, ok := b.lookup[upd.Key]; ok {
		els := nodes[upd.NodeID]
		// Find and remove element from slice (order doesn't matter)
		for i, e := range els {
			if e == el {
				els[i] = els[len(els)-1]
				els = els[:len(els)-1]
				break
			}
		}

		nodes[upd.NodeID] = els
		if len(els) == 0 {
			delete(nodes, upd.NodeID)
		}
		if len(nodes) == 0 {
			delete(b.lookup, upd.Key)
		}
	}

	return upd
}

// GetCovering returns all updates for a specific nodeID whose ranges overlap with the query range.
// This is the main operation for finding missing updates to sync with other peers.
// Uses binary search on the per-nodeID range index for O(log m) lookup.
//
// Time complexity: O(log m + k) where m = updates for nodeID, k = query results
func (b *Buffer) GetCovering(nodeID string, r structs.Range) []*types.Update {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()

	idx, ok := b.nodeIDIdx[nodeID]
	if !ok || idx == nil || len(idx.updates) == 0 {
		return nil
	}

	var result []*types.Update

	// Binary search: find first update where Range.Start <= r.End
	// All updates before this position have Range.Start > r.End (no overlap possible)
	startIdx := sort.Search(len(idx.updates), func(i int) bool {
		return idx.updates[i].Range.Start <= r.End
	})

	// Linear scan from startIdx: updates are sorted by Range.Start
	// We can stop early when Range.Start > r.End (no more overlaps possible)
	for i := startIdx; i < len(idx.updates); i++ {
		upd := idx.updates[i]

		// Early exit: since updates are sorted, remaining updates cannot overlap
		if upd.Range.Start > r.End {
			break
		}

		// Check for actual range overlap
		if b.rangesOverlap(r, upd.Range) {
			result = append(result, upd)
		}
	}

	return result
}

// rangesOverlap checks if two ranges have any intersection.
// Ranges [a, b] and [c, d] overlap if: a <= d && c <= b
func (b *Buffer) rangesOverlap(r1, r2 structs.Range) bool {
	return r1.Start <= r2.End && r2.Start <= r1.End
}

// PeekN returns the first n most recently added elements without modifying LRU state.
// Elements are returned in reverse insertion order (most recent first).
//
// Time complexity: O(n)
func (b *Buffer) PeekN(n int) []*types.Update {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()

	if n > b.items.Len() {
		n = b.items.Len()
	}

	res := make([]*types.Update, 0, n)
	el := b.items.Front() // Front = most recent

	for i := 0; i < n && el != nil; i++ {
		if upd, ok := el.Value.(*types.Update); ok {
			res = append(res, upd)
		}
		el = el.Next()
	}

	return res
}

// Len returns the current number of elements in the buffer.
// This is the total element count, not deduplicated by key.
//
// Time complexity: O(1)
func (b *Buffer) Len() int {
	b.rwlock.RLock()
	defer b.rwlock.RUnlock()
	return b.items.Len()
}

// addToNodeIDIndex adds updates to the per-nodeID range index.
// Maintains sorted invariant by Range.Start using binary search.
// Called after collapse: adds only the final merged updates.
//
// Time complexity: O(k log m) where k = new updates, m = existing for nodeID
func (b *Buffer) addToNodeIDIndex(nodeID string, updates []*types.Update) {
	if len(updates) == 0 {
		return
	}

	idx, ok := b.nodeIDIdx[nodeID]
	if !ok {
		idx = &nodeRangeIndex{
			updates: make([]*types.Update, 0, 10),
		}
		b.nodeIDIdx[nodeID] = idx
	}

	// Insert each update at its sorted position
	for _, upd := range updates {
		// Binary search: find insertion point to maintain sorted order
		insertPos := sort.Search(len(idx.updates), func(i int) bool {
			return idx.updates[i].Range.Start >= upd.Range.Start
		})

		// Insert at position (go idiom for slice insertion)
		idx.updates = append(idx.updates[:insertPos],
			append([]*types.Update{upd}, idx.updates[insertPos:]...)...)
	}
}

// removeFromNodeIDIndex removes updates corresponding to list elements from the range index.
// Called when elements are removed from the LRU list.
//
// Time complexity: O(k log m) where k = elements to remove, m = total for nodeID
func (b *Buffer) removeFromNodeIDIndex(nodeID string, els []*list.Element) {
	if len(els) == 0 {
		return
	}

	for _, el := range els {
		b.removeFromNodeIDIndexLocked(nodeID, el.Value.(*types.Update))
	}
}

// removeFromNodeIDIndexLocked removes a single update from the nodeID range index.
// Performs linear search to find the update to remove (acceptable since typically k << m).
//
// Time complexity: O(m) where m = updates for nodeID
// Note: Could be optimized to O(log m) with pointer-based lookup if needed
func (b *Buffer) removeFromNodeIDIndexLocked(nodeID string, upd *types.Update) {
	idx, ok := b.nodeIDIdx[nodeID]
	if !ok || idx == nil || len(idx.updates) == 0 {
		return
	}

	// Find and remove the matching update by comparing all fields
	for i, u := range idx.updates {
		if u.Range.Start == upd.Range.Start &&
			u.Range.End == upd.Range.End &&
			u.Key == upd.Key &&
			u.NodeID == upd.NodeID {
			// Remove element (order maintained by sorted invariant)
			idx.updates = append(idx.updates[:i], idx.updates[i+1:]...)
			break
		}
	}

	// Clean up empty index entry
	if len(idx.updates) == 0 {
		delete(b.nodeIDIdx, nodeID)
	}
}
