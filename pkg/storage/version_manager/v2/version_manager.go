package v2

import (
	"context"
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
)

// VersionManager v2 - оптимизированная версия без глобального лока
// Ключевые отличия от v1:
// - Advance() полностью lock-free (seq атомарен, history для локальной ноды не используется)
// - Update() параллельно обрабатывает updates через worker pool
// - History шардирован по nodeID с per-node локами
type VersionManager struct {
	nodeID  string
	seq     atomic.Uint64           // локальный sequence number, всегда contiguous
	history *history.ShardedHistory // только для remote нод
	engine  engine.Engine
	fabric  crdt.CRDTFabric
	updater *entry_updater.EntryUpdater
}

// NewVersionManager создаёт новый VersionManager v2
func NewVersionManager(nodeID string, engine engine.Engine) *VersionManager {
	return &VersionManager{
		nodeID:  nodeID,
		history: history.NewShardedHistory(),
		engine:  engine,
		fabric:  crdt.NewFabric(),
		updater: entry_updater.NewEntryUpdater(crdt.NewFabric(), nodeID),
	}
}

// Advance увеличивает локальный sequence number на 1
// Полностью lock-free: seq атомарен, история для локальной ноды не хранится
// (локальный seq всегда contiguous: 1, 2, 3, ...)
func (vm *VersionManager) Advance() uint64 {
	return vm.seq.Add(1)
}

// Update применяет набор updates от remote нод
// Возвращает slice успешно применённых updates
func (vm *VersionManager) Update(ctx context.Context, updates ...*types.Update) []*types.Update {
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
func (vm *VersionManager) updateSequential(ctx context.Context, updates []*types.Update) []*types.Update {
	applied := make([]*types.Update, 0, len(updates))

	for _, update := range updates {
		if vm.handleUpdate(ctx, update) {
			applied = append(applied, update)
		}
	}

	return applied
}

// updateParallel параллельно применяет updates через worker pool
func (vm *VersionManager) updateParallel(ctx context.Context, updates []*types.Update) []*types.Update {
	type result struct {
		update  *types.Update
		applied bool
	}

	jobs := make(chan *types.Update, len(updates))
	results := make(chan result, len(updates))

	// Запускаем workers
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for update := range jobs {
				applied := vm.handleUpdate(ctx, update)
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

	applied := make([]*types.Update, 0, len(updates))
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
	// TryAddRange атомарно проверяет и добавляет range в history
	// Если range уже был применён - возвращает false
	if !vm.history.TryAddRange(update.NodeID, update.Range) {
		return false
	}

	// Применяем update к engine
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
		slog.ErrorContext(ctx, "VersionManager.handleUpdate: failed to update entry",
			"error", err,
			"key", update.Key,
			"update_type", update.Type)
		return false
	}

	// Если entry не найдена и это не delete - создаём новую
	if !updated && update.Type != types.UpdateTypeDelete {
		return vm.handleNewEntry(ctx, update)
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

	err := wal.ReplayAll(ctx, func(u *types.Update) error {
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
			vm.history.AddRange(u.NodeID, u.Range)
		}

		// Применяем update к engine
		vm.applyUpdateDuringRestore(ctx, u)

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
		slog.ErrorContext(ctx, "VersionManager.applyUpdateDuringRestore: failed to update entry",
			"error", err,
			"key", update.Key)
		return
	}

	if !updated && update.Type != types.UpdateTypeDelete {
		vm.handleNewEntry(ctx, update)
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
