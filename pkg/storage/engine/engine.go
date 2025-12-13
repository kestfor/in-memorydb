package engine

import (
	"context"
	"github/kestfor/in-memorydb/pkg/crdt"
	"github/kestfor/in-memorydb/pkg/crdt/hlc"
	"sync"
)

//go:generate mockgen -source=engine.go -destination=mocks/engine.mock.go Engine

type CRDTEntry struct {
	Mu           sync.RWMutex
	Object       crdt.CRDT      // сам CRDT-объект (интерфейс)
	Tombstone    bool           // для удалений
	SetTimeStamp *hlc.Timestamp // timestamp of last set action
}

func (e *CRDTEntry) Deleted() bool {
	return e.Tombstone
}

func (e *CRDTEntry) DeletedAt() *hlc.Timestamp {
	return e.SetTimeStamp
}

// UpdateFunc - функция для безопасного обновления entry
// Возвращает true если изменения должны быть сохранены
type UpdateFunc = func(ctx context.Context, entry *CRDTEntry) (modified bool, err error)

// CreateFunc is a function type that constructs and returns a new CRDTEntry or an error during initialization.
type CreateFunc = func(ctx context.Context) (*CRDTEntry, error)

type Callback = func(entry *CRDTEntry)

type Engine interface {
	Start(ctx context.Context) error
	Stop()

	// Get returns entry with specified key, if entry was marked as deleted, but physically present, method should return nil.
	// For modifying key that still present use Update instead
	Get(ctx context.Context, key string) (*CRDTEntry, bool)
	Clock() *hlc.Time

	Put(ctx context.Context, key string, obj crdt.CRDT, callback Callback) *hlc.Timestamp
	PutWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string, obj crdt.CRDT, callback Callback) *hlc.Timestamp

	Delete(ctx context.Context, key string) (*CRDTEntry, bool)
	DeleteWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string) (*CRDTEntry, bool)

	// Update выполняет atomic update entry через callback
	// Возвращает true если entry был модифицирован
	Update(ctx context.Context, key string, updateFunc UpdateFunc) (modified bool, err error)

	// GetOrCreate получает существующий entry или создаёт новый
	// CreateFunc вызывается только при создании
	GetOrCreate(ctx context.Context, key string, createFunc CreateFunc) (*CRDTEntry, bool, error)
}
