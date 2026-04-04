package v2

import (
	"context"
	"sync"
	"testing"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyDigests(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)
	ctx := context.Background()

	// Create actual entries via Update so CRDT state hashes are computed
	for i, key := range []string{"key1", "key2", "key3"} {
		counter := crdt.NewPNCounter("remote")
		delta := counter.Increment(int64(i + 1))
		update := types.Update{
			NodeID:       "remote",
			Seq:          uint64(i + 1),
			Key:          key,
			Type:         types.UpdateTypeSet,
			TimeStamp:    hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote"},
			SetTimeStamp: hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote"},
			Payload:      delta,
		}
		applied := vm.Update(ctx, update)
		require.Len(t, applied, 1)
	}

	// Collect digests from all buckets
	allDigests := make(map[string]uint64)
	for b := uint32(0); b < vm.NumBuckets(); b++ {
		for k, v := range vm.KeyDigests(b) {
			allDigests[k] = v
		}
	}
	assert.Len(t, allDigests, 3)

	// All hashes should be non-zero (based on CRDT state hash)
	for _, h := range allDigests {
		assert.NotEqual(t, uint64(0), h)
	}
}

func TestKeyDigestsEmpty(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)

	digests := vm.KeyDigests(0)
	assert.Len(t, digests, 0)
}

func TestMergeKeyState_NewKey(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	counter := crdt.NewPNCounter("remote")
	counter.Increment(42)
	stateBytes, err := counter.MarshalJSON()
	require.NoError(t, err)

	ks := &types.KeyState{
		Key:          "new-key",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        stateBytes,
		Tombstone:    false,
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
	}

	err = vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	entry, ok := eng.Get(ctx, "new-key")
	require.True(t, ok)
	assert.NotNil(t, entry)
	assert.Equal(t, int64(42), entry.Object.Value())
}

func TestMergeKeyState_ExistingKey_MergeCRDT(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	// Create local counter with value 10
	localCounter := crdt.NewPNCounter("local")
	localCounter.Increment(10)
	eng.PutWithTimeStamp(ctx, hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "local"}, "key1", localCounter, nil)
	vm.Advance("key1")

	// Create remote counter with value 20
	remoteCounter := crdt.NewPNCounter("remote")
	remoteCounter.Increment(20)
	remoteState, err := remoteCounter.MarshalJSON()
	require.NoError(t, err)

	ks := &types.KeyState{
		Key:          "key1",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        remoteState,
		Tombstone:    false,
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
	}

	err = vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	entry, ok := eng.Get(ctx, "key1")
	require.True(t, ok)
	// Merged: max(10, 0) from local + max(0, 20) from remote = 30
	assert.Equal(t, int64(30), entry.Object.Value())
}

func TestMergeKeyState_Tombstone_RemoteNewer(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	// Create local entry
	localCounter := crdt.NewPNCounter("local")
	localCounter.Increment(5)
	eng.PutWithTimeStamp(ctx, hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "local"}, "key1", localCounter, nil)

	// Remote sends tombstone with newer timestamp
	remoteState, _ := crdt.NewPNCounter("remote").MarshalJSON()
	ks := &types.KeyState{
		Key:          "key1",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        remoteState,
		Tombstone:    true,
		SetTimeStamp: hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "remote"},
	}

	err := vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	// Entry should be deleted (tombstoned)
	_, ok := eng.Get(ctx, "key1")
	assert.False(t, ok)
}

func TestStateDigestDeterministic(t *testing.T) {
	h1 := stateDigest(12345, false)
	h2 := stateDigest(12345, false)
	assert.Equal(t, h1, h2)
}

func TestStateDigestDifferentForDifferentStates(t *testing.T) {
	h1 := stateDigest(12345, false)
	h2 := stateDigest(12346, false)
	assert.NotEqual(t, h1, h2)

	// Same hash, different tombstone
	h3 := stateDigest(12345, true)
	assert.NotEqual(t, h1, h3)
}

func TestConcurrentAdvanceAndDigests(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(256), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)

	const numGoroutines = 50
	const opsPerGoroutine = 100

	var wg sync.WaitGroup

	// Writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				vm.Advance("key")
			}
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				vm.KeyDigests(uint32(j) % vm.NumBuckets())
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, uint64(numGoroutines*opsPerGoroutine), vm.GetCurrentSequence())
}
