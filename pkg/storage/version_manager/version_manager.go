package version_manager

import (
	"context"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/storage/history"
	"in-memorydb/pkg/storage/types"
	"in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	"log/slog"
	"sync"
	"sync/atomic"
)

type VersionManager struct {
	nodeID  string           // unique ID of current node
	seq     atomic.Uint64    // global sequence number of updates for current node
	history *history.History // nodeID -> seq range
	engine  *engine.Engine   // thread-safe for read/write, for entry use each entry has its own mutex
	fabric  crdt.CRDTFabric  // thread-safe
	mu      sync.RWMutex
}

func NewVersionManager(nodeID string, engine *engine.Engine) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		history: history.NewHistory(),
		engine:  engine,
		fabric:  crdt.NewFabric(),
	}
}

// getVersion возвращает текущую версию для указанного узла без захвата мьютекса
func (vm *VersionManager) getVersion(nodeID string) structs.Range {
	if nodeID == vm.nodeID {
		return structs.Range{End: vm.seq.Load()}
	}
	vclock := vm.history.VectorClockContiguous()
	return structs.Range{End: vclock[nodeID]}
}

// Advance увеличивает локальный счетчик обновлений на 1
func (vm *VersionManager) Advance() uint64 {
	return vm.seq.Add(1)
}

// Update applies a set of updates to the version manager while maintaining thread safety using a mutex lock.
// Returns slice of applied updates
func (vm *VersionManager) Update(updates ...*types.Update) []*types.Update {
	vm.mu.Lock()
	applied := make([]*types.Update, 0, len(updates)/2+1) // approximate
	for _, update := range updates {
		if vm.handleUpdate(update, false) {
			applied = append(applied, update)
		}
	}
	vm.mu.Unlock()
	return applied
}

func (vm *VersionManager) VectorClockContiguous() types.VectorClock {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	vc := vm.history.VectorClockContiguous()
	vc[vm.nodeID] = vm.seq.Load()
	return vc
}

func (vm *VersionManager) VectorClockMax() types.VectorClock {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	vc := vm.history.VectorClockMax()
	vc[vm.nodeID] = vm.seq.Load()
	return vc
}

//
//// GetKnownNodes возвращает список всех известных нод (включая локальную)
//// Нода становится "известной" после получения от неё хотя бы одного update
//func (vm *VersionManager) GetKnownNodes() []string {
//	vm.mu.RLock()
//	defer vm.mu.RUnlock()
//
//	nodes := make([]string, 0, len(vm.version)+1)
//	nodes = append(nodes, vm.nodeID)
//
//	for nodeID := range vm.version {
//		nodes = append(nodes, nodeID)
//	}
//
//	return nodes
//}

// GetCurrentSequence возвращает текущий sequence number локальной ноды
func (vm *VersionManager) GetCurrentSequence() uint64 {
	return vm.seq.Load()
}

func (vm *VersionManager) VersionDiff(remote map[string]uint64) map[string][]structs.Range {
	return vm.history.DiffAll(remote)
}

// handleUpdate processes a single update, applying changes to the VersionManager.
// Returns true if the update was successfully applied, otherwise false.
// If fromWAl flag set as true, updates sets with update's timestamp
func (vm *VersionManager) handleUpdate(update *types.Update, fromWAL bool) bool {

	// old update, already applied
	if vm.history.HasRange(update.NodeID, update.Range) {
		return false
	}

	vm.history.AddRange(update.NodeID, update.Range)

	key := update.Key
	entry, ok := vm.engine.Get(key)

	// key doesn't present
	if !ok && update.Type != types.UpdateTypeDelete {
		// TODO рассмотреть этот кейс
		slog.Warn("WARNING, edge case, key may have been deleted after this delta", "key", key, "node", update.NodeID)

		newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)

		if err != nil {
			slog.Error("error while creating new CRDT from delta", "err", err, "update", update)
			return false
		}

		if update.Type == types.UpdateTypeDelta {
			err = newCRDT.ApplyDelta(update.Payload)
			if err != nil {
				slog.Error("error while applying delta", "err", err, "update", update)
			}
		}

		if fromWAL {
			vm.engine.PutWithTimeStamp(key, update.TimeStamp, newCRDT)
		} else {
			vm.engine.Put(key, newCRDT)
		}

		return true

	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()

	// update timestamp newer than existed
	if fromWAL || entry.LastUpdated.Before(update.TimeStamp) {
		switch update.Type {
		case types.UpdateTypeSet: // here payload is nil

			newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)
			if err != nil {
				slog.Error("error while creating new CRDT from delta", "err", err, "update", update)
				return false
			}

			entry.Object = newCRDT
			if fromWAL {
				entry.LastUpdated = update.TimeStamp
			} else {
				entry.LastUpdated = vm.engine.Clock().SyncWithRemote(update.TimeStamp)
			}

		case types.UpdateTypeDelete:
			entry.Object = nil
			entry.Tombstone = true
		case types.UpdateTypeDelta:
			err := entry.Object.ApplyDelta(update.Payload)
			if err != nil {
				slog.Error("error while applying delta update", "err", err, "update", update)
				return false
			}
			entry.LastUpdated = vm.engine.Clock().SyncWithRemote(update.TimeStamp) // TODO check if needed here
		default:
			slog.Warn("unexpected update type", "type", update.Type)
			return false
		}
		return true
	}

	return false
}

// RestoreFromWal iterates through all saved in wal and apply them
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error {
	count := 0
	err := wal.ReplayAll(func(u *types.Update) error {

		count++

		if count%100 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		if u.NodeID == vm.nodeID {
			vm.Advance()
		}
		vm.handleUpdate(u, true)

		return nil
	})
	return err
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
