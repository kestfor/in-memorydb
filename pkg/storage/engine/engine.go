package engine

import (
	"context"
	"sync"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
)

//go:generate mockgen -source=engine.go -destination=mocks/engine.mock.go Engine

type CRDTEntry struct {
	Mu           sync.RWMutex
	Object       crdt.CRDT
	Tombstone    bool
	SetTimeStamp hlc.Timestamp
}

type SnapshotEntry struct {
	Key          string
	Object       crdt.CRDT
	Tombstone    bool
	SetTimeStamp hlc.Timestamp
	State        []byte
	Hash         uint64
}

type Stats struct {
	Shards     int
	Keys       int
	Tombstones int
}

func (e *CRDTEntry) Deleted() bool {
	return e.Tombstone
}

func (e *CRDTEntry) DeletedAt() hlc.Timestamp {
	return e.SetTimeStamp
}

type UpdateFunc = func(ctx context.Context, entry *CRDTEntry) (modified bool, err error)

type CreateFunc = func(ctx context.Context) (*CRDTEntry, error)

type Callback = func(entry *CRDTEntry)

type Engine interface {
	Start(ctx context.Context) error
	Stop()
	Get(ctx context.Context, key string) (*CRDTEntry, bool)
	GetRaw(ctx context.Context, key string) (*CRDTEntry, bool)
	Clock() *hlc.Time
	Put(ctx context.Context, key string, obj crdt.CRDT, callback Callback) hlc.Timestamp
	PutWithTimeStamp(ctx context.Context, ts hlc.Timestamp, key string, obj crdt.CRDT, callback Callback) hlc.Timestamp
	Delete(ctx context.Context, key string) (*CRDTEntry, bool)
	DeleteWithTimeStamp(ctx context.Context, ts hlc.Timestamp, key string) (*CRDTEntry, bool)
	Update(ctx context.Context, key string, updateFunc UpdateFunc) (modified bool, err error)
	GetOrCreate(ctx context.Context, key string, createFunc CreateFunc) (*CRDTEntry, bool, error)
	Snapshot(ctx context.Context, includeTombstones bool) []SnapshotEntry
	Stats(ctx context.Context) Stats
}
