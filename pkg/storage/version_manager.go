package storage

import (
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/structs"
	"log/slog"
	"sync"
	"sync/atomic"
)

type Version map[string]structs.Range

type VersionManager struct {
	nodeID  string          // unique ID of current node
	seq     atomic.Int64    // global sequence number of updates for current node
	version Version         // nodeID -> seq range
	engine  *Engine         // thread-safe for read/write, for entry use each entry has its own mutex
	fabric  crdt.CRDTFabric // thread-safe
	mu      sync.RWMutex
}

func NewVersionManager(nodeID string, engine *Engine) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		version: make(Version),
		engine:  engine,
		fabric:  crdt.NewFabric(),
	}
}

// getVersion возвращает текущую версию для указанного узла без захвата мьютекса
func (vm *VersionManager) getVersion(nodeID string) structs.Range {
	if nodeID == vm.nodeID {
		return structs.Range{End: vm.seq.Load()}
	}
	return vm.version[nodeID]
}

// Advance увеличивает локальный счетчик обновлений на 1
func (vm *VersionManager) Advance() {
	vm.seq.Add(1)
}

// Update applies a set of updates to the version manager while maintaining thread safety using a mutex lock.
// Returns slice of applied updates
func (vm *VersionManager) Update(updates ...*Update) []*Update {
	vm.mu.Lock()
	applied := make([]*Update, 0, len(updates)/2) // approximate
	for _, update := range updates {
		if vm.handleUpdate(update) {
			applied = append(applied, update)
		}
	}
	vm.mu.Unlock()
	return applied
}

// GetVersionVector возвращает текущий version vector всех известных нод
// Это snapshot текущего состояния синхронизации
func (vm *VersionManager) GetVersionVector() map[string]structs.Range {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	vec := make(map[string]structs.Range, len(vm.version)+1)

	// Добавляем локальную ноду
	vec[vm.nodeID] = structs.Range{End: vm.seq.Load()}

	// Копируем версии других нод
	for nodeID, seq := range vm.version {
		vec[nodeID] = seq
	}

	return vec
}

// GetKnownNodes возвращает список всех известных нод (включая локальную)
// Нода становится "известной" после получения от неё хотя бы одного update
func (vm *VersionManager) GetKnownNodes() []string {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	nodes := make([]string, 0, len(vm.version)+1)
	nodes = append(nodes, vm.nodeID)

	for nodeID := range vm.version {
		nodes = append(nodes, nodeID)
	}

	return nodes
}

// RegisterNode явно добавляет ноду в список известных с начальной версией 0
// Полезно для инициализации при присоединении к кластеру
func (vm *VersionManager) RegisterNode(nodeID string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if nodeID == vm.nodeID {
		return // не регистрируем самих себя
	}

	if _, exists := vm.version[nodeID]; !exists {
		vm.version[nodeID] = structs.Range{}
	}
}

// GetCurrentSequence возвращает текущий sequence number локальной ноды
func (vm *VersionManager) GetCurrentSequence() int64 {
	return vm.seq.Load()
}

// handleUpdate processes a single update, applying changes to the VersionManager.
// Returns true if the update was successfully applied, otherwise false.
func (vm *VersionManager) handleUpdate(update *Update) bool {
	currVersion := vm.getVersion(update.NodeID)

	// old update, already applied
	if update.Range.End <= currVersion.End {
		return false
	}

	key := update.Key
	entry, ok := vm.engine.Get(key)

	// key doesn't present
	if !ok {
		// TODO рассмотреть этот кейс
		slog.Warn("WARNING, edge case, key may have been deleted after this delta", "key", key, "node", update.NodeID)

		newCRDT, err := update.Payload.CreateCRDT()
		if err != nil {
			slog.Error("error while creating new CRDT from delta", "err", err, "update", update)
			return false
		}
		vm.engine.Put(key, newCRDT)
		return true

	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()

	// update timestamp newer than existed
	if entry.LastUpdated.Before(update.TimeStamp) {
		switch update.Type {
		case UpdateTypeSet:

			newCRDT, err := update.Payload.CreateCRDT()
			if err != nil {
				slog.Error("error while creating new CRDT from delta", "err", err, "update", update)
				return false
			}

			entry.Object = newCRDT
			entry.LastUpdated = vm.engine.clock.SyncWithRemote(update.TimeStamp)

		case UpdateTypeDelete:
			entry.Object = nil
			entry.Tombstone = true
		case UpdateTypeDelta:
			err := entry.Object.ApplyDelta(update.Payload)
			if err != nil {
				slog.Error("error while applying delta update", "err", err, "update", update)
				return false
			}
		default:
			slog.Warn("unexpected update type", "type", update.Type)
			return false
		}
		return true
	}

	return false
}

//func (vm *VersionManager) handleUpdate(update Update) {
//	version := update
//	delta := update.Payload
//
//	missed, maxID, newNodeVersion := vm.getVersionSeqInfo(version)
//
//	vm.maxLastSeen[version.ReplicaID] = max(vm.maxLastSeen[version.ReplicaID], maxID)
//
//	if vm.getVersion(version.ReplicaID) >= maxID {
//		return
//	}
//	// инициализация если нужно
//	if _, ok := vm.missedUpdates[version.ReplicaID]; !ok {
//		vm.missedUpdates[version.ReplicaID] = structs.NewSet[int64]()
//	}
//
//	// добавляем пропущенные in-place
//	for id := range missed.All() {
//		vm.missedUpdates[version.ReplicaID].Add(id)
//	}
//
//	vm.version[version.ReplicaID] = newNodeVersion
//
//	entry, ok := vm.engine.Get(update.Key)
//
//	entry.Mu.Lock()
//	unlocked := false
//	if ok && entry.Object.Type() != delta.Type() {
//
//		if entry.LastUpdated.After(update.Timestamp) { // означает что локальный тип объекта более актуален
//			entry.Mu.Unlock()
//			return
//		} else {
//			ok = false
//			unlocked = true
//			entry.Mu.Unlock()
//		}
//
//	}
//
//	if !unlocked {
//		entry.Mu.Unlock()
//	}
//
//	if !ok {
//
//		newCRDT, err := vm.fabric.New(delta.Type(), vm.nodeID)
//		if err != nil {
//			// TODO : handle error, no CRDT found, should not happen but may if nodes use different versions of fabric, must be forbidden
//		}
//
//		vm.engine.Put(update.Key, newCRDT)
//		entry, ok = vm.engine.Get(update.Key)
//
//		if !ok {
//			// TODO : handle error, no entry found, should not happen
//		}
//
//	}
//
//	// TODO update if needed after DELETE logic is implemented in api
//
//	entry.Mu.Lock()
//	defer entry.Mu.Unlock()
//
//	err := entry.Object.ApplyDelta(delta)
//	if err != nil {
//		// TODO : handle error, should not happen
//	}
//
//	for _, id := range update.Version.Sequence {
//		vm.missedUpdates[version.ReplicaID].Delete(id) // удаляем те которые сейчас приняли
//	}
//
//	if len(missed) > 0 {
//		slog.Warn("detected missed updates",
//			"source_node", version.ReplicaID,
//			"missed", missed.Slice(),
//		)
//	}
//
//	vm.engine.clock.SyncWithRemote(update.Timestamp)
//	entry.LastUpdated = vm.engine.clock.Now()
//}

//func (vm *VersionManager) getVersionSeqInfo(version *Version) (missed structs.Set[int64], maxId int64, newNodeVersion int64) {
//	missed = structs.NewSet[int64]()
//	currVersion := vm.getVersion(version.ReplicaID)
//
//	slices.Sort(version.Sequence) // сортируем чтобы не было проблем с порядком
//	maxId = version.Sequence[len(version.Sequence)-1]
//
//	lastId := currVersion
//	cont := true
//
//	for _, id := range version.Sequence {
//		if id <= currVersion {
//			continue // outdated update
//		}
//
//		if id > lastId+1 {
//			for i := lastId + 1; i < id; i++ {
//				missed.Add(i)
//			}
//			cont = false
//		} else {
//			if cont {
//				currVersion++
//			}
//		}
//		lastId = id
//	}
//
//	newNodeVersion = currVersion
//	return missed, maxId, newNodeVersion
//
//}
