package v1

import (
	"context"
	"github/kestfor/in-memorydb/pkg/crdt"
	"github/kestfor/in-memorydb/pkg/storage/engine"
	"github/kestfor/in-memorydb/pkg/storage/version_manager/v1/entry_updater"
	"github/kestfor/in-memorydb/pkg/storage/version_manager/v1/history"
	"github/kestfor/in-memorydb/pkg/storage/wal"
	"github/kestfor/in-memorydb/pkg/structs"
	types "github/kestfor/in-memorydb/pkg/types"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Stats возвращает статистику VersionManager
type Stats struct {
	CurrentSequence uint64
	VectorClock     types.VectorClock
	HistorySize     int
}

type VersionManager struct {
	nodeID  string           // unique ID of current node
	seq     atomic.Uint64    // global sequence number of updates for current node
	history *history.History // nodeID -> seq range
	engine  engine.Engine    // thread-safe for read/write, for entry use each entry has its own mutex
	fabric  crdt.CRDTFabric  // thread-safe
	updater *entry_updater.EntryUpdater
	mu      sync.RWMutex
}

func NewVersionManager(nodeID string, engine engine.Engine) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		history: history.NewHistory(),
		engine:  engine,
		fabric:  crdt.NewFabric(),
		updater: entry_updater.NewEntryUpdater(crdt.NewFabric(), nodeID),
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
	vm.mu.Lock()
	res := vm.seq.Add(1)
	defer vm.mu.Unlock()
	vm.history.Add(vm.nodeID, res)
	return res
}

// Update applies a set of updates to the version manager while maintaining thread safety using a mutex lock.
// Returns slice of applied updates
func (vm *VersionManager) Update(ctx context.Context, updates ...*types.Update) []*types.Update {
	if len(updates) == 0 {
		return nil
	}

	vm.mu.Lock()
	applied := make([]*types.Update, 0, len(updates)/2+1) // approximate
	for _, update := range updates {
		if vm.handleUpdate(ctx, update) {
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
func (vm *VersionManager) VersionDiff(remote types.VectorClock) map[string][]structs.Range {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	res := vm.history.DiffAll(remote)
	//delete(res, vm.nodeID)
	return res
}

func (vm *VersionManager) handleUpdate(ctx context.Context, update *types.Update) bool {
	// Проверяем что update не был применён ранее
	if vm.history.HasRange(update.NodeID, update.Range) {
		return false
	}

	// Добавляем в историю
	vm.history.AddRange(update.NodeID, update.Range)

	updateEntryCallback := func(ctx context.Context, entry *engine.CRDTEntry) (bool, error) {
		result := vm.updater.ApplyUpdate(entry, update)

		if result.Error != nil {
			return false, result.Error
		}

		if result.Applied && update.Type == types.UpdateTypeDelete {
			vm.engine.DeleteWithTimeStamp(ctx, update.SetTimeStamp, update.Key)
		}

		return result.Applied, nil
	}

	updated, err := vm.engine.Update(ctx, update.Key, updateEntryCallback)

	if err != nil {
		slog.ErrorContext(ctx, "VersionManager.handleUpdate: failed to update entry", "error", err, "key", update.Key, "update_type", update.Type)
		return false
	}

	// no entry found or already deleted
	if !updated && update.Type != types.UpdateTypeDelete {
		return vm.handleNewEntry(ctx, update)
	}

	return true
}

func (vm *VersionManager) handleNewEntry(ctx context.Context, update *types.Update) bool {
	newEntry, err := vm.updater.CreateFromUpdate(update)

	if err != nil {
		slog.Error("VersionManager.handleNewEntry: failed to create entry",
			"error", err,
			"key", update.Key,
			"type", update.Type)
		return false
	}

	vm.engine.PutWithTimeStamp(ctx, update.SetTimeStamp, update.Key, newEntry.Object, nil)
	return true
}

//// handleExistingEntry обновляет существующий entry
//func (vm *VersionManager) handleExistingEntry(ctx context.Context, entry *engine.CRDTEntry, update *types.Update) (bool, error) {
//	// Применяем обновление через EntryUpdater
//	result := vm.updater.ApplyUpdate(entry, update)
//
//	if result.Error != nil {
//		// Логируем только неожиданные ошибки
//		if !errors.Is(result.Error, entry_updater.ErrOldUpdate) {
//			slog.Warn("VersionManager.handleExistingEntry: update failed",
//				"error", result.Error,
//				"key", update.Key,
//				"update_type", update.Type)
//		}
//		return false,
//	}
//
//	if result.Applied && update.Type == types.UpdateTypeDelete {
//		vm.engine.DeleteWithTimeStamp(ctx, update.TimeStamp, update.Key)
//	}
//
//	return result.Applied
//}

// TODO починить возможные мутации тут когда могут прийти разные типы дельт в разное время, как вариант через эпоху

// handleUpdate processes a single update, applying changes to the VersionManager.
// Returns true if the update was successfully applied, otherwise false.
// If fromWAl flag set as true, updates sets with update's timestamp
// conflict resolution flow:
//
//  1. If update's ts before entry's set_ts -> old update, not applied
//  2. If update's ts after entry's set_ts ->
//  1. if update's set_ts > entry's set_ts -> new crdt created, delta applied
//  2. if update's set_ts <= entry's set_ts -> delta applied
//func (vm *VersionManager) handleUpdate(ctx context.Context, update *types.Update, fromWAL bool) bool {
//
//	// old update, already applied
//	if vm.history.HasRange(update.NodeID, update.Range) {
//		return false
//	}
//
//	vm.history.AddRange(update.NodeID, update.Range)
//
//	key := update.Key
//	entry, ok := vm.engine.Get(context.TODO(), key)
//
//	if !ok && update.Type == types.UpdateTypeDelete {
//		return true
//	}
//
//	// key doesn't present
//	if !ok {
//		return vm.handleSetNotExist(update, fromWAL)
//	}
//
//	entry.Mu.Lock()
//
//	// update timestamp newer than existed >=
//	if !entry.SetTimeStamp.After(update.TimeStamp) {
//		switch update.Type {
//		case types.UpdateTypeSet:
//			defer entry.Mu.Unlock()
//			return vm.handleSet(entry, update, fromWAL)
//
//		case types.UpdateTypeDelta:
//			defer entry.Mu.Unlock()
//			return vm.handleDelta(entry, update, fromWAL)
//
//		case types.UpdateTypeDelete:
//			entry.Mu.Unlock()
//			vm.engine.DeleteWithTimeStamp(ctx, update.SetTimeStamp, key)
//			return true
//
//		default:
//			slog.Warn("version_manager.handleUpdate: unexpected update type", "type", update.Type)
//			return false
//		}
//	}
//
//	return false
//}
//
//func (vm *VersionManager) handleSetNotExist(update *types.Update, fromWAL bool) (ok bool) {
//	newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)
//
//	if err != nil {
//		slog.Error("version_manager.handleUpdate: error while creating new CRDT from delta", "err", err, "update", update)
//		return false
//	}
//
//	if update.Type == types.UpdateTypeDelta {
//		err = newCRDT.ApplyDelta(update.Payload)
//		if err != nil {
//			slog.Error("version_manager.handleUpdate: error while applying delta", "err", err, "update", update)
//		}
//	}
//
//	vm.engine.PutWithTimeStamp(context.TODO(), update.SetTimeStamp.Copy(), update.Key, newCRDT, nil)
//
//	return true
//}
//
//func (vm *VersionManager) handleSet(entry *engine.CRDTEntry, update *types.Update, fromWAL bool) (ok bool) {
//	newCRDT, err := vm.fabric.New(update.Payload.Type(), vm.nodeID)
//	if err != nil {
//		slog.Error("version_manager.handleSet: error while creating new CRDT from delta", "err", err, "update", update)
//		return false
//	}
//
//	entry.Object = newCRDT
//	entry.Tombstone = false
//
//	if fromWAL {
//		entry.SetTimeStamp = update.SetTimeStamp.Copy()
//	} else {
//		entry.SetTimeStamp = update.SetTimeStamp.Copy()
//	}
//
//	return true
//}
//
//func (vm *VersionManager) handleDelta(entry *engine.CRDTEntry, update *types.Update, fromWAL bool) (ok bool) {
//	if entry.Object.Type() != update.Payload.Type() {
//
//		if update.SetTimeStamp.After(entry.SetTimeStamp) {
//			vm.handleSet(entry, update, fromWAL)
//		}
//
//	} else if !entry.SetTimeStamp.After(update.SetTimeStamp) {
//		vm.handleSet(entry, update, fromWAL)
//	}
//
//	err := entry.Object.ApplyDelta(update.Payload)
//	entry.Tombstone = false
//
//	if err != nil {
//		slog.Error("version_manager.handleDelta: error while applying delta update", "err", err, "update", update)
//		return false
//	}
//
//	return true
//}

// RestoreFromWal iterates through all saved in wal and apply them
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error {
	count := 0
	localUpdatesNumber := 0
	vm.mu.Lock()
	err := wal.ReplayAll(ctx, func(u *types.Update) error {

		count++

		if count%100 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		vm.handleUpdate(ctx, u)

		if u.NodeID == vm.nodeID {
			localUpdatesNumber++
		}

		return nil
	})
	vm.seq.Add(uint64(localUpdatesNumber))
	vm.mu.Unlock()

	if err != nil {
		slog.Error("VersionManager.RestoreFromWAL: failed to replay WAL",
			"error", err,
			"processed_updates", count)
		return err
	}

	slog.Info("VersionManager.RestoreFromWAL: completed", "processed_updates", count)

	return err
}

func (vm *VersionManager) Stats() Stats {
	vm.mu.RLock()
	defer vm.mu.RUnlock()

	return Stats{
		CurrentSequence: vm.seq.Load(),
		VectorClock:     vm.VectorClockContiguous(),
		// HistorySize можно добавить в history если нужно
	}
}
