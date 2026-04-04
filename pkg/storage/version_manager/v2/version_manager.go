package v2

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager/v1/entry_updater"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2/history"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
)

const (
	// parallelThreshold - минимальное количество updates для параллельной обработки
	parallelThreshold = 10
	// numWorkers - количество воркеров для параллельной обработки
	numWorkers = 8
	// defaultNumBuckets - number of hash buckets for partitioned anti-entropy
	DefaultNumBuckets = 4
)

// keyMeta holds precomputed hash for anti-entropy digest comparison
type keyMeta struct {
	mu     sync.RWMutex
	hash   uint64 // stateDigest(CRDT.Hash(), tombstone) — updated on every mutation
	bucket uint32
}

// VersionManager v2 - оптимизированная версия без глобального лока
// Ключевые отличия от v1:
// - Advance() полностью lock-free (seq атомарен, history для локальной ноды не используется)
// - Update() параллельно обрабатывает updates через worker pool
// - History шардирован по nodeID с per-node локами
// - Per-key version clocks for key-based anti-entropy
type VersionManager struct {
	nodeID      string
	seq         atomic.Uint64           // локальный sequence number, всегда contiguous
	history     *history.ShardedHistory // только для remote нод
	engine      engine.Engine
	fabric      crdt.CRDTFabric
	updater     *entry_updater.EntryUpdater
	keyVersions sync.Map // key string → *keyMeta
	numBuckets  uint32   // number of hash buckets for partitioned anti-entropy
}

// NewVersionManager создаёт новый VersionManager v2
func NewVersionManager(nodeID string, engine engine.Engine) *VersionManager {
	return &VersionManager{
		nodeID:     nodeID,
		history:    history.NewShardedHistory(),
		engine:     engine,
		fabric:     crdt.NewFabric(),
		updater:    entry_updater.NewEntryUpdater(crdt.NewFabric(), nodeID),
		numBuckets: DefaultNumBuckets,
	}
}

// keyBucket computes the hash bucket for a key
func keyBucket(key string, numBuckets uint32) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32() % numBuckets
}

// Advance увеличивает локальный sequence number на 1
// Полностью lock-free: seq атомарен, история для локальной ноды не хранится
// (локальный seq всегда contiguous: 1, 2, 3, ...)
func (vm *VersionManager) Advance(key string) uint64 {
	return vm.seq.Add(1)
}

// Update применяет набор updates от remote нод
// Возвращает slice успешно применённых updates
func (vm *VersionManager) Update(ctx context.Context, updates ...types.Update) []types.Update {
	if len(updates) == 0 {
		return nil
	}

	// Для небольших батчей - последовательная обработка
	if len(updates) < parallelThreshold {
		return vm.updateSequential(ctx, updates)
	}

	// Для больших батчей - параллельная обработка
	return vm.updateParallel(ctx, updates)
}

// updateSequential последовательно применяет updates
func (vm *VersionManager) updateSequential(ctx context.Context, updates []types.Update) []types.Update {
	applied := make([]types.Update, 0, len(updates))

	for _, update := range updates {
		if vm.handleUpdate(ctx, &update) {
			applied = append(applied, update)
		}
	}

	return applied
}

// updateParallel параллельно применяет updates через worker pool
func (vm *VersionManager) updateParallel(ctx context.Context, updates []types.Update) []types.Update {
	type result struct {
		update  types.Update
		applied bool
	}

	jobs := make(chan types.Update, len(updates))
	results := make(chan result, len(updates))

	// Запускаем workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for update := range jobs {
				applied := vm.handleUpdate(ctx, &update)
				results <- result{update, applied}
			}
		}()
	}

	// Отправляем задания
	for _, update := range updates {
		jobs <- update
	}
	close(jobs)

	// Ждём завершения и собираем результаты
	go func() {
		wg.Wait()
		close(results)
	}()

	applied := make([]types.Update, 0, len(updates))
	for r := range results {
		if r.applied {
			applied = append(applied, r.update)
		}
	}

	return applied
}

// handleUpdate обрабатывает один update
// Возвращает true если update был применён
func (vm *VersionManager) handleUpdate(ctx context.Context, update *types.Update) bool {
	//TryAddRange атомарно проверяет и добавляет range в history
	//Если range уже был применён - возвращает false
	if !vm.history.TryAddRange(update.NodeID, structs.Range{update.Seq, update.Seq}) {
		return false
	}

	// Применяем update к engine
	var newHash uint64
	updateEntryCallback := func(ctx context.Context, entry *engine.CRDTEntry) (bool, error) {
		result := vm.updater.ApplyUpdate(entry, update)

		if result.Error != nil {
			return false, result.Error
		}

		if result.Modified && update.Type == types.UpdateTypeDelete {
			vm.engine.DeleteWithTimeStamp(ctx, update.SetTimeStamp, update.Key)
		}

		if result.Applied {
			newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
		}

		return result.Applied, nil
	}

	updated, err := vm.engine.Update(ctx, update.Key, updateEntryCallback)

	if err != nil {
		slog.ErrorContext(ctx, "VersionManager.handleUpdate: failed to update entry",
			"error", err,
			"key", update.Key,
			"update_type", update.Type)
		return false
	}

	// Если entry не найдена и это не delete - создаём новую
	if !updated && update.Type != types.UpdateTypeDelete {
		if !vm.handleNewEntry(ctx, update) {
			return false
		}
	} else {
		vm.updateKeyStateHash(update.Key, newHash)
	}

	return true
}

// handleNewEntry создаёт новую entry из update
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
	vm.updateKeyStateHash(update.Key, stateDigest(newEntry.Object.Hash(), false))
	return true
}

// VectorClockContiguous возвращает vector clock с contiguous seq для каждой ноды
func (vm *VersionManager) VectorClockContiguous() types.VectorClock {
	vc := vm.history.AllContiguousSeq()
	// Для локальной ноды seq всегда contiguous
	vc[vm.nodeID] = vm.seq.Load()
	return vc
}

// VectorClockMax возвращает vector clock с max seq для каждой ноды
func (vm *VersionManager) VectorClockMax() types.VectorClock {
	vc := vm.history.AllMaxSeq()
	vc[vm.nodeID] = vm.seq.Load()
	return vc
}

// VersionDiff вычисляет ranges которые есть у remote но отсутствуют локально
func (vm *VersionManager) VersionDiff(remote types.VectorClock) map[string][]structs.Range {
	return vm.history.DiffAll(remote)
}

// GetCurrentSequence возвращает текущий sequence number локальной ноды
func (vm *VersionManager) GetCurrentSequence() uint64 {
	return vm.seq.Load()
}

// RestoreFromWal восстанавливает состояние из WAL
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error {
	count := 0
	localUpdatesNumber := uint64(0)

	err := wal.ReplayAll(ctx, func(u types.Update) error {
		count++

		// Периодически проверяем cancellation
		if count%100 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		// Для локальных updates - только считаем
		if u.NodeID == vm.nodeID {
			localUpdatesNumber++
		} else {
			// Для remote - добавляем в history
			vm.history.Add(u.NodeID, u.Seq)
		}

		// Применяем update к engine
		vm.applyUpdateDuringRestore(ctx, &u)

		return nil
	})

	// Устанавливаем seq для локальной ноды
	vm.seq.Store(localUpdatesNumber)

	if err != nil {
		slog.Error("VersionManager.RestoreFromWal: failed to replay WAL",
			"error", err,
			"processed_updates", count)
		return err
	}

	slog.Info("VersionManager.RestoreFromWal: completed", "processed_updates", count)
	return nil
}

// applyUpdateDuringRestore применяет update во время восстановления из WAL
// Отличается от handleUpdate тем, что не проверяет дубликаты (WAL уже уникален)
func (vm *VersionManager) applyUpdateDuringRestore(ctx context.Context, update *types.Update) {
	var newHash uint64
	updateEntryCallback := func(ctx context.Context, entry *engine.CRDTEntry) (bool, error) {
		result := vm.updater.ApplyUpdate(entry, update)

		if result.Error != nil {
			return false, result.Error
		}

		if result.Modified && update.Type == types.UpdateTypeDelete {
			vm.engine.DeleteWithTimeStamp(ctx, update.SetTimeStamp, update.Key)
		}

		if result.Applied {
			newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
		}

		return result.Applied, nil
	}

	updated, err := vm.engine.Update(ctx, update.Key, updateEntryCallback)

	if err != nil {
		slog.ErrorContext(ctx, "VersionManager.applyUpdateDuringRestore: failed to update entry",
			"error", err,
			"key", update.Key)
		return
	}

	if !updated && update.Type != types.UpdateTypeDelete {
		vm.handleNewEntry(ctx, update)
	} else {
		vm.updateKeyStateHash(update.Key, newHash)
	}

}

// RestoreSeq очищает историю для ноды (для перезапуска синхронизации)
func (vm *VersionManager) RestoreSeq(nodeID string) {
	vm.history.Clear(nodeID)
}

// Stats возвращает статистику VersionManager
type Stats struct {
	CurrentSequence uint64
	VectorClock     types.VectorClock
}

func (vm *VersionManager) Stats() Stats {
	return Stats{
		CurrentSequence: vm.seq.Load(),
		VectorClock:     vm.VectorClockContiguous(),
	}
}

// KeyDigests returns a map of key → hash for keys in the specified bucket
func (vm *VersionManager) KeyDigests(bucket uint32) map[string]uint64 {
	result := make(map[string]uint64)
	vm.keyVersions.Range(func(k, v any) bool {
		km := v.(*keyMeta)
		if km.bucket != bucket {
			return true
		}
		km.mu.RLock()
		result[k.(string)] = km.hash
		km.mu.RUnlock()
		return true
	})
	return result
}

// NumBuckets returns the number of hash buckets used for partitioned anti-entropy
func (vm *VersionManager) NumBuckets() uint32 {
	return vm.numBuckets
}

// MergeKeyState merges a remote key state into the local engine
func (vm *VersionManager) MergeKeyState(ctx context.Context, state *types.KeyState) error {
	if state == nil {
		return nil
	}

	remoteCRDT, err := vm.fabric.New(state.CRDTType, vm.nodeID)
	if err != nil {
		return err
	}
	if err := remoteCRDT.UnmarshalJSON(state.State); err != nil {
		return err
	}

	var newHash uint64

	entry, ok := vm.engine.GetRaw(ctx, state.Key)
	if !ok {
		// Key doesn't exist locally (physically deleted or never existed) — create it
		if state.Tombstone {
			vm.engine.PutWithTimeStamp(ctx, state.SetTimeStamp, state.Key, remoteCRDT, nil)
			vm.engine.DeleteWithTimeStamp(ctx, state.SetTimeStamp, state.Key)
			newHash = stateDigest(remoteCRDT.Hash(), true)
		} else {
			vm.engine.PutWithTimeStamp(ctx, state.SetTimeStamp, state.Key, remoteCRDT, nil)
			newHash = stateDigest(remoteCRDT.Hash(), false)
		}
	} else {
		entry.Mu.Lock()

		if entry.Tombstone && !state.Tombstone {
			// Local is tombstone, remote is alive — remote wins if newer
			if !state.SetTimeStamp.Before(entry.SetTimeStamp) {
				entry.Object = remoteCRDT
				entry.SetTimeStamp = state.SetTimeStamp
				entry.Tombstone = false
			}
			newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
			entry.Mu.Unlock()
		} else if !entry.Tombstone && state.Tombstone {
			// Local is alive, remote is tombstone — tombstone wins if newer
			if !state.SetTimeStamp.Before(entry.SetTimeStamp) {
				entry.Mu.Unlock()
				vm.engine.DeleteWithTimeStamp(ctx, state.SetTimeStamp, state.Key)
				newHash = stateDigest(remoteCRDT.Hash(), true)
			} else {
				// Local is alive and newer — merge CRDT state from remote (pre-delete state)
				if entry.Object.Type() == state.CRDTType {
					_ = entry.Object.Merge(remoteCRDT)
				}
				newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
				entry.Mu.Unlock()
			}
		} else if entry.Tombstone && state.Tombstone {
			// Both tombstones — keep the one with newer timestamp
			if state.SetTimeStamp.After(entry.SetTimeStamp) {
				entry.SetTimeStamp = state.SetTimeStamp
			}
			newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
			entry.Mu.Unlock()
		} else {
			// Both alive — merge CRDT states
			if entry.Object.Type() == state.CRDTType {
				if err := entry.Object.Merge(remoteCRDT); err != nil {
					entry.Mu.Unlock()
					slog.ErrorContext(ctx, "VersionManager.MergeKeyState: failed to merge",
						"error", err, "key", state.Key)
					return err
				}
				// Keep the newer SetTimeStamp — if remote did a Set later, adopt its timestamp
				if state.SetTimeStamp.After(entry.SetTimeStamp) {
					entry.SetTimeStamp = state.SetTimeStamp
				}
			} else {
				// Type mismatch: newer SetTimeStamp wins
				if !state.SetTimeStamp.IsZero() && (entry.SetTimeStamp.IsZero() || entry.SetTimeStamp.Before(state.SetTimeStamp)) {
					entry.Object = remoteCRDT
					entry.SetTimeStamp = state.SetTimeStamp
				}
			}
			newHash = stateDigest(entry.Object.Hash(), entry.Tombstone)
			entry.Mu.Unlock()
		}
	}

	vm.updateKeyStateHash(state.Key, newHash)

	return nil
}

// stateDigest computes a combined hash of the CRDT state hash and tombstone flag.
// This is the value stored in keyMeta.hash and compared during anti-entropy.
func stateDigest(crdtHash uint64, tombstone bool) uint64 {
	h := fnv.New64a()
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, crdtHash)
	h.Write(buf)
	if tombstone {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// updateKeyStateHash updates the precomputed state hash for a key
func (vm *VersionManager) updateKeyStateHash(key string, hash uint64) {
	val, _ := vm.keyVersions.LoadOrStore(key, &keyMeta{
		bucket: keyBucket(key, vm.numBuckets),
	})
	km := val.(*keyMeta)
	km.mu.Lock()
	km.hash = hash
	km.mu.Unlock()
}
