package storage

import (
	"in-memorydb/pkg/crdt"
	"sync"
	"sync/atomic"
)

type CRDTEntry struct {
	Mu          sync.RWMutex
	Type        crdt.CRDTType     // enum: GCounter, ORSet, LWWReg, RGA и т.д. // хранится внутри CRDT, поэтому возможно не нужен тут
	Object      crdt.CRDT         // сам CRDT-объект (интерфейс)
	Deltas      []crdt.Delta      // накопленные дельты для репликации (delta buffer) лучше в отдельном месте
	Context     CausalContext     // causal metadata (vector clock / dot context)
	Tombstone   bool              // для удалений
	LastUpdated crdt.HLCTimestamp // HLC или unix nano
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

	// статистика
	countKeys atomic.Int64
}
