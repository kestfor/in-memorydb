package storage

import (
	"in-memorydb/pkg/crdt"
	"sync"
	"sync/atomic"
)

type VersionContext struct {
	// for future backward compatibility
}

type Version struct {
	ReplicaID string
	Sequence  int64
	Context   VersionContext
}

type Update struct {
	Key       string
	Delta     crdt.Delta
	Version   *Version
	Timestamp *crdt.Timestamp
}

// TODO нужна очередь которая будет хранить пропущенные обновления
type VersionManager struct {
	mutex   sync.RWMutex     // protects version
	nodeID  string           // unique ID of current node
	seq     atomic.Int64     // global sequence number of updates for current node
	version map[string]int64 // nodeID -> seq
	engine  *Engine          // thread-safe for read/write, for entry use each entry has its own mutex
	fabric  crdt.CRDTFabric  // thread-safe
}

func NewVersionManager(nodeID string, engine *Engine) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		version: make(map[string]int64),
		engine:  engine,
		fabric:  crdt.NewFabric(),
	}
}

// GetVersion возвращает текущую версию для указанного узла
func (vm *VersionManager) GetVersion(nodeID string) int64 {
	if nodeID == vm.nodeID {
		return vm.seq.Load()
	}

	vm.mutex.RLock()
	defer vm.mutex.RUnlock()
	return vm.version[nodeID]
}

// Advance увеличивает локальный счетчик обновлений на 1
func (vm *VersionManager) Advance() {
	vm.seq.Add(1)
}

func (vm *VersionManager) Update(updates ...Update) {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	for _, update := range updates {
		vm.handleUpdate(update)
	}

}

// handleUpdate обрабатывает обновление, обновляет локальную версию и применяет дельту к объекту, mutex должен быть захвачен ранее
func (vm *VersionManager) handleUpdate(update Update) {
	version := update.Version
	delta := update.Delta

	// TODO как проверять порядок, seq могут идти не последовательно, могут быть пропуски

	if vm.version[version.ReplicaID] < version.Sequence {

		// TODO если версия больше чем локальная могут быть пропуски, нужно их обработать
		vm.version[version.ReplicaID] = version.Sequence

		entry, ok := vm.engine.Get(version.ReplicaID)

		entry.Mu.Lock()

		if ok && entry.Object.Type() != delta.Type() {

			if entry.LastUpdated.After(update.Timestamp) { // означает что локальный тип объекта более актуален
				entry.Mu.Unlock()
				return
			} else {
				ok = false
				entry.Mu.Unlock()
			}

		}

		if !ok {

			newCRDT, err := vm.fabric.New(delta.Type(), vm.nodeID)
			if err != nil {
				// TODO : handle error, no CRDT found, should not happen but may if nodes use different versions of fabric, must be forbidden
			}

			vm.engine.Put(update.Key, newCRDT)
			entry, ok = vm.engine.Get(update.Key)

			if !ok {
				// TODO : handle error, no entry found, should not happen
			}

		}

		// TODO update if needed after DELETE logic is implemented in api

		entry.Mu.Lock()
		defer entry.Mu.Unlock()

		err := entry.Object.ApplyDelta(delta)
		if err != nil {
			// TODO : handle error, should not happen
		}

		vm.engine.clock.SyncWithRemote(update.Timestamp)
		entry.LastUpdated = vm.engine.clock.Now()

	}

	// TODO отложенный буфер если понадобится

}
