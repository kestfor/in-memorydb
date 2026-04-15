package version_manager

import (
	"context"

	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
)

//go:generate mockgen -source=version_manager.go -destination=mocks/version_manager.mock.go VersionManager

type Stats struct {
	CurrentSequence       uint64            `json:"current_sequence"`
	VectorClockContiguous types.VectorClock `json:"vector_clock_contiguous"`
	VectorClockMax        types.VectorClock `json:"vector_clock_max"`
	TrackedKeys           int               `json:"tracked_keys"`
	NumBuckets            uint32            `json:"num_buckets"`
}

type VersionManager interface {
	UpdateLocal(ctx context.Context, updates ...types.Update) []types.Update
	UpdateRemote(ctx context.Context, updates ...types.Update) []types.Update
	VectorClockContiguous() types.VectorClock
	VectorClockMax() types.VectorClock
	VersionDiff(remote types.VectorClock) map[string][]structs.Range
	RestoreFromWal(ctx context.Context, wal wal.WAL) error
	KeyDigests(bucket uint32) map[string]uint64
	MergeKeyState(ctx context.Context, state *types.KeyState) error
	Stats() Stats
}
