package anti_entropy

import (
	"context"
	"log/slog"

	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager"
	"github.com/kestfor/in-memorydb/pkg/types"
)

// Service encapsulates the core anti-entropy logic:
// reading key states, comparing digests, and merging remote states.
// Used by gRPC server (for remote calls) and directly in tests.
type Service struct {
	engine engine.Engine
	vm     version_manager.VersionManager
}

func NewService(engine engine.Engine, vm version_manager.VersionManager) *Service {
	return &Service{engine: engine, vm: vm}
}

// KeyDigests returns per-key digest hashes for the specified bucket.
func (s *Service) KeyDigests(bucket uint32) map[string]uint64 {
	return s.vm.KeyDigests(bucket)
}

// CollectKeyStates reads full CRDT states (including tombstones) for the given keys.
func (s *Service) CollectKeyStates(ctx context.Context, keys []string) []*types.KeyState {
	states := make([]*types.KeyState, 0, len(keys))

	for _, key := range keys {
		entry, ok := s.engine.GetRaw(ctx, key)
		if !ok {
			continue
		}

		entry.Mu.RLock()
		stateBytes, err := entry.Object.MarshalJSON()
		if err != nil {
			entry.Mu.RUnlock()
			slog.ErrorContext(ctx, "anti_entropy.CollectKeyStates: failed to marshal CRDT state", "key", key, "error", err)
			continue
		}
		ks := &types.KeyState{
			Key:          key,
			CRDTType:     entry.Object.Type(),
			State:        stateBytes,
			Tombstone:    entry.Tombstone,
			SetTimeStamp: entry.SetTimeStamp,
		}
		entry.Mu.RUnlock()

		states = append(states, ks)
	}

	return states
}

// FindStaleKeys compares remote digests with local ones for a given bucket.
// Returns keys where local state differs from or is missing vs remote.
func (s *Service) FindStaleKeys(localDigests, remoteDigests map[string]uint64) []string {
	var staleKeys []string
	for key, remoteHash := range remoteDigests {
		localHash, exists := localDigests[key]
		if !exists || localHash != remoteHash {
			staleKeys = append(staleKeys, key)
		}
	}
	return staleKeys
}

// MergeKeyStates applies remote key states to the local node.
// Returns the list of keys that were successfully merged.
func (s *Service) MergeKeyStates(ctx context.Context, states []*types.KeyState) []string {
	var merged []string
	for _, ks := range states {
		if err := s.vm.MergeKeyState(ctx, ks); err != nil {
			slog.ErrorContext(ctx, "anti_entropy.MergeKeyStates: failed to merge",
				"error", err, "key", ks.Key)
			continue
		}
		merged = append(merged, ks.Key)
	}
	return merged
}
