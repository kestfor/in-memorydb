package v2

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
)

// keyMeta holds per-key state for anti-entropy.
// hash is updated on every mutation; bucket is immutable after first insert.
// Using atomic.Uint64 for hash eliminates the per-key mutex from the hot write path.
type keyMeta struct {
	hash   atomic.Uint64
	bucket uint32
}

// keyVersionShards is a sharded concurrent map from string → *keyMeta.
//
// Compared to sync.Map it avoids the periodic dirty→read flush that causes
// unpredictable latency spikes under write-heavy workloads.
// Each shard has its own RWMutex so contention is spread across numVersionShards locks.
//
// Hot path for an existing key (updateKeyStateHash):
//
//	RLock → lookup → RUnlock → atomic.Store   (no write lock at all)
//
// Insert path (new key, happens during preload only):
//
//	RLock → miss → RUnlock → Lock → double-check → insert → Unlock
const numVersionShards = 64

type keyVersionShards struct {
	shards [numVersionShards]kvShard
}

type kvShard struct {
	mu   sync.RWMutex
	data map[string]*keyMeta
}

func newKeyVersionShards() *keyVersionShards {
	s := &keyVersionShards{}
	for i := range s.shards {
		s.shards[i].data = make(map[string]*keyMeta)
	}
	return s
}

func (s *keyVersionShards) shardFor(key string) *kvShard {
	h := fnv.New32a()
	h.Write([]byte(key))
	return &s.shards[h.Sum32()%numVersionShards]
}

// LoadOrStore returns the existing *keyMeta for key, or stores and returns a new
// one initialised with the given bucket. The fast path (existing key) only
// acquires a read lock.
func (s *keyVersionShards) LoadOrStore(key string, bucket uint32) *keyMeta {
	sh := s.shardFor(key)

	// Fast path: key already exists — read lock only.
	sh.mu.RLock()
	if km, ok := sh.data[key]; ok {
		sh.mu.RUnlock()
		return km
	}
	sh.mu.RUnlock()

	// Slow path: insert under write lock with double-check.
	sh.mu.Lock()
	if km, ok := sh.data[key]; ok {
		sh.mu.Unlock()
		return km
	}
	km := &keyMeta{bucket: bucket}
	sh.data[key] = km
	sh.mu.Unlock()
	return km
}

// Range calls fn for every key in the map.
// Locks one shard at a time so writers to other shards are never blocked.
func (s *keyVersionShards) Range(fn func(key string, km *keyMeta) bool) {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.RLock()
		for k, v := range sh.data {
			if !fn(k, v) {
				sh.mu.RUnlock()
				return
			}
		}
		sh.mu.RUnlock()
	}
}
