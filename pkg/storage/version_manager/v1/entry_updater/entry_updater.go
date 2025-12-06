package entry_updater

import (
	"errors"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/types"
	"log/slog"
)

var (
	ErrTypeMismatch = errors.New("CRDT type mismatch")
	ErrApplyDelta   = errors.New("failed to apply delta")
	ErrCreateCRDT   = errors.New("failed to create CRDT")
)

type UpdateResult struct {
	Applied  bool
	Modified bool
	Error    error
}

// EntryUpdater инкапсулирует логику обновления Entry
// Отделяет VersionManager от деталей работы с Entry
type EntryUpdater struct {
	fabric crdt.CRDTFabric
	nodeID string
}

func NewEntryUpdater(fabric crdt.CRDTFabric, nodeID string) *EntryUpdater {
	return &EntryUpdater{
		fabric: fabric,
		nodeID: nodeID,
	}
}

// ApplyUpdate применяет обновление к существующему entry
// Entry должен быть залочен вызывающей стороной
func (eu *EntryUpdater) ApplyUpdate(entry *engine.CRDTEntry, update *types.Update) UpdateResult {

	// если entry была создана после того, как появился update -> do nothing
	if entry.SetTimeStamp.After(update.TimeStamp) {
		return UpdateResult{Applied: true}
	}

	switch update.Type {
	case types.UpdateTypeSet:
		return eu.applySet(entry, update)
	case types.UpdateTypeDelta:
		return eu.applyDelta(entry, update)
	case types.UpdateTypeDelete:
		return eu.applyDelete(entry, update)
	default:
		slog.Warn("EntryUpdater.ApplyUpdate: unknown update type", "type", update.Type)
		return UpdateResult{Applied: false, Error: errors.New("unknown update type")}
	}

}

// CreateFromUpdate создаёт новый Entry из update (когда ключа нет)
func (eu *EntryUpdater) CreateFromUpdate(update *types.Update) (*engine.CRDTEntry, error) {
	newCRDT, err := eu.fabric.New(update.Payload.Type(), eu.nodeID)
	if err != nil {
		slog.Error("EntryUpdater.CreateFromUpdate: failed to create CRDT",
			"error", err, "type", update.Payload.Type())
		return nil, errors.Join(ErrCreateCRDT, err)
	}

	// Для Delta update применяем delta
	if update.Type == types.UpdateTypeDelta {
		if err := newCRDT.ApplyDelta(update.Payload); err != nil {
			slog.Error("EntryUpdater.CreateFromUpdate: failed to apply delta",
				"error", err)
			return nil, errors.Join(ErrApplyDelta, err)
		}
	}

	return &engine.CRDTEntry{
		Object:       newCRDT,
		SetTimeStamp: update.SetTimeStamp.Copy(),
		Tombstone:    false,
	}, nil
}

func (eu *EntryUpdater) applySet(entry *engine.CRDTEntry, update *types.Update) UpdateResult {

	// этот set происходит до того, как была создана entry
	if update.SetTimeStamp.Before(entry.SetTimeStamp) {
		return UpdateResult{Applied: true}
	}

	newCRDT, err := eu.fabric.New(update.Payload.Type(), eu.nodeID)
	if err != nil {
		slog.Error("EntryUpdater.applySet: failed to create CRDT",
			"error", err, "type", update.Payload.Type())
		return UpdateResult{Applied: false, Error: errors.Join(ErrCreateCRDT, err)}
	}

	// Заменяем объект
	entry.Object = newCRDT
	entry.SetTimeStamp = update.SetTimeStamp.Copy()
	entry.Tombstone = false

	return UpdateResult{Applied: true, Modified: true}
}

func (eu *EntryUpdater) applyDelta(entry *engine.CRDTEntry, update *types.Update) UpdateResult {
	// Проверяем совместимость типов
	if entry.Object.Type() != update.Payload.Type() {
		// Если SetTimeStamp update новее - это новый объект, делаем Set
		if update.SetTimeStamp.After(entry.SetTimeStamp) {
			res := eu.applySet(entry, update)

			if !res.Applied {
				return res
			}

			// теперь типы совпадают, можно делать apply заново
			return eu.applyDelta(entry, update)

		}

		// Иначе устаревший тип
		slog.Debug("EntryUpdater.applyDelta: type mismatch, ignoring old update",
			"entry_type", entry.Object.Type(),
			"update_type", update.Payload.Type(),
			"update_set_timestamp", update.SetTimeStamp,
			"entry_set_timestamp", entry.SetTimeStamp)

		return UpdateResult{Applied: true}
	}

	// Если SetTimeStamp update новее - создаём новый объект
	if update.SetTimeStamp.After(entry.SetTimeStamp) {
		return eu.applySet(entry, update)
	}

	// Применяем delta к существующему объекту
	if err := entry.Object.ApplyDelta(update.Payload); err != nil {
		slog.Error("EntryUpdater.applyDelta: failed to apply delta",
			"error", err)
		return UpdateResult{Applied: false, Error: errors.Join(ErrApplyDelta, err)}
	}

	entry.Tombstone = false
	return UpdateResult{Applied: true, Modified: true}
}

func (eu *EntryUpdater) applyDelete(entry *engine.CRDTEntry, update *types.Update) UpdateResult {
	entry.Tombstone = true
	entry.SetTimeStamp = update.SetTimeStamp.Copy()
	return UpdateResult{Applied: true, Modified: true}
}
