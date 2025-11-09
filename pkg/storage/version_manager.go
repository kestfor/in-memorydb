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
	Delta   crdt.Delta
	Version Version
}

type VersionManager struct {
	mutex   sync.RWMutex
	nodeID  string
	seq     atomic.Int64
	version map[string]Version
}

func NewVersionManager(nodeID string) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		version: make(map[string]Version),
	}
}

func (vm *VersionManager) GetVersion(replicaID string) Version {
	vm.mutex.RLock()
	defer vm.mutex.RUnlock()
	return vm.version[replicaID]
}

func (vm *VersionManager) Advance() {
	vm.mutex.Lock()
	defer vm.mutex.Unlock()

	seq := vm.seq.Add(1)
	vm.version[vm.nodeID] = Version{
		ReplicaID: vm.nodeID,
		Sequence:  seq,
		Context:   VersionContext{},
	}
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
	//delta := update.Delta

	if vm.version[version.ReplicaID].Sequence < version.Sequence {
		vm.version[version.ReplicaID] = version
		// TODO : update engine, apply delta
	}
}
