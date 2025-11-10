package storage

import (
	"fmt"
	"in-memorydb/pkg/crdt"
	"log/slog"
	"sync"
	"sync/atomic"
)

// scaleThreshold — при каком количестве ключей на шард начинаем увеличивать
const scaleThreshold = 100_000

type CRDTEntry struct {
	Mu          sync.RWMutex
	Object      crdt.CRDT       // сам CRDT-объект (интерфейс)
	Tombstone   bool            // для удалений
	LastUpdated *crdt.Timestamp // timestamp of last update
}

type CausalContext struct {
	// VectorClock map[string]int64 // векторный час
	// DotContext  map[string]int64 // dot context
}

type Shard struct {
	mu   sync.RWMutex
	data map[string]*CRDTEntry
}

type Engine struct {
	nodeID     string
	shards     atomic.Pointer[[]*Shard]
	numShards  atomic.Uint32
	growthLock sync.Mutex
	clock      *crdt.Time
	// статистика
	countKeys atomic.Int64
}

func NewEngine(initialShards int, nodeID string) *Engine {
	if initialShards <= 0 {
		initialShards = 64
	}
	s := &Engine{}
	shards := make([]*Shard, initialShards)
	s.shards.Store(&shards)
	s.numShards.Store(uint32(initialShards))
	s.clock = crdt.NewHLC(nodeID)
	return s
}

func (e *Engine) Get(key string) (*CRDTEntry, bool) {
	shard := e.shardFor(key)
	shard.mu.RLock()
	entry, ok := shard.data[key]
	shard.mu.RUnlock()
	return entry, ok
}

func (e *Engine) Put(key string, obj crdt.CRDT) {
	shard := e.shardFor(key)

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if _, ok := shard.data[key]; !ok {
		e.countKeys.Add(1)
	}

	shard.data[key] = &CRDTEntry{
		Object:      obj,
		LastUpdated: e.clock.Now(),
	}

	e.maybeScale()
}

func (e *Engine) Delete(key string) {
	shard := e.shardFor(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if _, ok := shard.data[key]; ok {
		delete(shard.data, key)
		e.countKeys.Add(-1)
	}
}

func (e *Engine) shardFor(key string) *Shard {
	idx := hashKey(key) & (e.numShards.Load() - 1)
	arr := *e.shards.Load()

	shard := arr[idx]
	if shard != nil {
		return shard
	}

	// ленивое создание шарда
	e.growthLock.Lock()
	defer e.growthLock.Unlock()

	// double-checked
	if arr[idx] == nil {
		arr[idx] = &Shard{data: make(map[string]*CRDTEntry, 128)}
	}
	return arr[idx]
}

func (e *Engine) maybeScale() {
	total := e.countKeys.Load()
	nShards := int64(e.numShards.Load())

	if total/nShards > scaleThreshold {
		go e.growShards()
	}
}

func (e *Engine) growShards() {
	e.growthLock.Lock()
	defer e.growthLock.Unlock()

	current := e.numShards.Load()
	if total := e.countKeys.Load(); total/int64(current) < scaleThreshold {
		return // кто-то уже увеличил
	}

	newCount := current * 2
	oldArr := *e.shards.Load()
	newArr := make([]*Shard, newCount)

	// перемещаем старые шарды в новые позиции (ребаланс по хэшу)
	for _, old := range oldArr {
		if old == nil {
			continue
		}
		old.mu.RLock()
		for k, v := range old.data {
			idx := hashKey(k) & (newCount - 1)
			if newArr[idx] == nil {
				newArr[idx] = &Shard{data: make(map[string]*CRDTEntry, 128)}
			}
			newArr[idx].data[k] = v
		}
		old.mu.RUnlock()
	}

	e.shards.Store(&newArr)
	e.numShards.Store(newCount)
	slog.Info(fmt.Sprintf("[store] scaled to %d shards\n", newCount))
}
