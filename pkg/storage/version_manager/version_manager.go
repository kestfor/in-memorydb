package version_manager

import (
	"context"

	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
)

//go:generate mockgen -source=version_manager.go -destination=mocks/version_manager.mock.go VersionManager

type VersionManager interface {
	Advance(key string) uint64
	Update(ctx context.Context, updates ...types.Update) []types.Update
	VectorClockContiguous() types.VectorClock
	VectorClockMax() types.VectorClock
	VersionDiff(remote types.VectorClock) map[string][]structs.Range
	RestoreFromWal(ctx context.Context, wal wal.WAL) error

	// Key-based anti-entropy methods
	KeyDigests(bucket uint32) map[string]uint64
	MergeKeyState(ctx context.Context, state *types.KeyState) error
}
