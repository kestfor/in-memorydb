package storage

import (
	"container/list"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/structs"
	"log/slog"
	"sync"
	"sync/atomic"

	"golang.org/x/exp/slices"
)

type VersionContext struct {
	// for future backward compatibility
}

type Version struct {
	ReplicaID string         // unique ID of node that created update
	Sequence  []int64        // ids of merged updates
	Context   VersionContext // for future backward compatibility
}

type Update struct {
	Key       string          // key of entry
	Delta     crdt.Delta      // merged updates
	Version   *Version        // version of update
	Timestamp *crdt.Timestamp // timestamp of update
}

type VersionManager struct {
	nodeID  string           // unique ID of current node
	seq     atomic.Int64     // global sequence number of updates for current node
	version map[string]int64 // nodeID -> seq
	engine  *Engine          // thread-safe for read/write, for entry use each entry has its own mutex
	fabric  crdt.CRDTFabric  // thread-safe
	mu      sync.Mutex

	missedUpdates   map[string]structs.Set[int64] // nodeID -> set of ids of missed updates
	bufferedUpdates map[string]list.List          // nodeID -> list of buffered updates // now unused
	maxLastSeen     map[string]int64              // nodeID -> max last seen sequence number
}

func NewVersionManager(nodeID string, engine *Engine) *VersionManager {
	return &VersionManager{
		nodeID:          nodeID,
		version:         make(map[string]int64),
		missedUpdates:   make(map[string]structs.Set[int64]),
		bufferedUpdates: make(map[string]list.List),
		maxLastSeen:     make(map[string]int64),
		engine:          engine,
		fabric:          crdt.NewFabric(),
	}
}

// getVersion возвращает текущую версию для указанного узла без захвата мьютекса
func (vm *VersionManager) getVersion(nodeID string) int64 {
	if nodeID == vm.nodeID {
		return vm.seq.Load()
	}
	return vm.version[nodeID]
}

// Advance увеличивает локальный счетчик обновлений на 1
func (vm *VersionManager) Advance() {
	vm.seq.Add(1)
}

func (vm *VersionManager) Update(updates ...Update) {
	vm.mu.Lock()
	for _, update := range updates {
		vm.handleUpdate(update)
	}
	vm.mu.Unlock()
}

func (vm *VersionManager) GetMissedIDs(nodeID string) (structs.Set[int64], bool) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	missed, ok := vm.missedUpdates[nodeID]
	return missed, ok
}

func (vm *VersionManager) handleUpdate(update Update) {
	version := update.Version
	delta := update.Delta

	missed, maxID, newNodeVersion := vm.getVersionSeqInfo(version)

	vm.maxLastSeen[version.ReplicaID] = max(vm.maxLastSeen[version.ReplicaID], maxID)

	if vm.getVersion(version.ReplicaID) >= maxID {
		return
	}
	// инициализация если нужно
	if _, ok := vm.missedUpdates[version.ReplicaID]; !ok {
		vm.missedUpdates[version.ReplicaID] = structs.NewSet[int64]()
	}

	// добавляем пропущенные in-place
	for id := range missed.All() {
		vm.missedUpdates[version.ReplicaID].Add(id)
	}

	vm.version[version.ReplicaID] = newNodeVersion

	entry, ok := vm.engine.Get(update.Key)

	entry.Mu.Lock()
	unlocked := false
	if ok && entry.Object.Type() != delta.Type() {

		if entry.LastUpdated.After(update.Timestamp) { // означает что локальный тип объекта более актуален
			entry.Mu.Unlock()
			return
		} else {
			ok = false
			unlocked = true
			entry.Mu.Unlock()
		}

	}

	if !unlocked {
		entry.Mu.Unlock()
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

	for _, id := range update.Version.Sequence {
		vm.missedUpdates[version.ReplicaID].Delete(id) // удаляем те которые сейчас приняли
	}

	if len(missed) > 0 {
		slog.Warn("detected missed updates",
			"source_node", version.ReplicaID,
			"missed", missed.Slice(),
		)
	}

	vm.engine.clock.SyncWithRemote(update.Timestamp)
	entry.LastUpdated = vm.engine.clock.Now()
}

func (vm *VersionManager) getVersionSeqInfo(version *Version) (missed structs.Set[int64], maxId int64, newNodeVersion int64) {
	missed = structs.NewSet[int64]()
	currVersion := vm.getVersion(version.ReplicaID)

	slices.Sort(version.Sequence) // сортируем чтобы не было проблем с порядком
	maxId = version.Sequence[len(version.Sequence)-1]

	lastId := currVersion
	cont := true

	for _, id := range version.Sequence {
		if id <= currVersion {
			continue // outdated update
		}

		if id > lastId+1 {
			for i := lastId + 1; i < id; i++ {
				missed.Add(i)
			}
			cont = false
		} else {
			if cont {
				currVersion++
			}
		}
		lastId = id
	}

	newNodeVersion = currVersion
	return missed, maxId, newNodeVersion

}
