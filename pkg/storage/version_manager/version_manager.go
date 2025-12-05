package version_manager

import (
	"context"
	"in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	"in-memorydb/pkg/types"
)

//go:generate mockgen -source=version_manager.go -destination=mocks/version_manager.mock.go VersionManager

type VersionManager interface {
	Advance() uint64
	Update(ctx context.Context, updates ...*types.Update) []*types.Update
	VectorClockContiguous() types.VectorClock
	VectorClockMax() types.VectorClock
	VersionDiff(remote types.VectorClock) map[string][]structs.Range
	RestoreFromWal(ctx context.Context, wal wal.WAL) error
}
