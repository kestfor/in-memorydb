package version_manager

import (
	"context"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/storage/history"
	"in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	types "in-memorydb/pkg/types"
	"log/slog"
	"sync"
	"sync/atomic"
)

// TODO не прикольно что логика с entry вынесена за engine, как вариант можно попробовать через callback добавлять нужную логику

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

func (vm *VersionManager) RestoreSeq(nodeID string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.history.Clear(nodeID)
}

// Advance увеличивает локальный счетчик обновлений на 1, добавляет в историю
func (vm *VersionManager) Advance() uint64 {
	res := vm.seq.Add(1)
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.history.Add(vm.nodeID, res)
	return res
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

// GetCurrentSequence возвращает текущий sequence number локальной ноды
func (vm *VersionManager) GetCurrentSequence() uint64 {
	return vm.seq.Load()
}

// VersionDiff computes the differences between the remote version vector and the local history, excluding the local node.
func (vm *VersionManager) VersionDiff(remote map[string]uint64) map[string][]structs.Range {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	res := vm.history.DiffAll(remote)
	//delete(res, vm.nodeID)
	return res
}

// TODO починить возможные мутации тут когда могут прийти разные типы дельт в разное время, как вариант через эпоху

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
	entry, ok := vm.engine.Get(context.TODO(), key)

	if !ok && update.Type == types.UpdateTypeDelete {
		return false
	}

	// key doesn't present
	if !ok {
		return vm.handleSetNotExist(update, fromWAL)
	}

	entry.Mu.Lock()
	defer entry.Mu.Unlock()

	// update timestamp newer than existed >=
	if !entry.LastUpdated.After(update.TimeStamp) {
		switch update.Type {
		case types.UpdateTypeSet:
			return vm.handleSet(entry, update, fromWAL)

		case types.UpdateTypeDelta:
			return vm.handleDelta(entry, update, fromWAL)

		case types.UpdateTypeDelete:
			vm.handleDelete(entry, update)
			return true

		default:
			slog.Warn("version_manager.handleUpdate: unexpected update type", "type", update.Type)
			return false
		}
	}

	return false
}

func (vm *VersionManager) handleSetNotExist(update *types.Update, fromWAL bool) (ok bool) {
	newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)

	if err != nil {
		slog.Error("version_manager.handleUpdate: error while creating new CRDT from delta", "err", err, "update", update)
		return false
	}

	if update.Type == types.UpdateTypeDelta {
		err = newCRDT.ApplyDelta(update.Payload)
		if err != nil {
			slog.Error("version_manager.handleUpdate: error while applying delta", "err", err, "update", update)
		}
	}

	if fromWAL {
		vm.engine.PutWithTimeStamp(context.TODO(), update.Key, update.TimeStamp.Copy(), newCRDT)
	} else {
		vm.engine.Put(context.TODO(), update.Key, newCRDT)
	}

	return true
}

func (vm *VersionManager) handleSet(entry *engine.CRDTEntry, update *types.Update, fromWAL bool) (ok bool) {
	newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)
	if err != nil {
		slog.Error("version_manager.handleSet: error while creating new CRDT from delta", "err", err, "update", update)
		return false
	}

	entry.Object = newCRDT
	entry.Tombstone = false

	if fromWAL {
		entry.LastUpdated = update.TimeStamp.Copy()
	}

	return true
}

func (vm *VersionManager) handleDelta(entry *engine.CRDTEntry, update *types.Update, fromWAL bool) (ok bool) {
	if entry.Object == nil || entry.Object.Type() != update.Payload.Type() {
		vm.handleSet(entry, update, fromWAL)
	}

	err := entry.Object.ApplyDelta(update.Payload)
	entry.Tombstone = false

	if err != nil {
		slog.Error("version_manager.handleDelta: error while applying delta update", "err", err, "update", update)
		return false
	}

	return true
}

func (vm *VersionManager) handleDelete(entry *engine.CRDTEntry, update *types.Update) {
	entry.Object = nil // for gc
	entry.Tombstone = true
	entry.LastUpdated = vm.engine.Clock().SyncWithRemote(update.TimeStamp)
}

// RestoreFromWal iterates through all saved in wal and apply them
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error {
	count := 0
	err := wal.ReplayAll(ctx, func(u *types.Update) error {

		count++

		if count%100 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		vm.handleUpdate(u, true)

		if u.NodeID == vm.nodeID {
			vm.Advance()
		}

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
