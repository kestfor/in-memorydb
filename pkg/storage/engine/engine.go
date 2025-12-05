package engine

import (
	"context"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/crdt/hlc"
	"sync"
)

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

type Callback = func(entry *CRDTEntry)

type Engine interface {
	Start(ctx context.Context) error
	Stop()

	Get(ctx context.Context, key string) (*CRDTEntry, bool)
	Clock() *hlc.Time

	Put(ctx context.Context, key string, obj crdt.CRDT, callback Callback) *hlc.Timestamp
	PutWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string, obj crdt.CRDT, callback Callback) *hlc.Timestamp

	Delete(ctx context.Context, key string) (*CRDTEntry, bool)
	DeleteWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string) (*CRDTEntry, bool)
}
