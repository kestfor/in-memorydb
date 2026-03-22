package v2

import (
	"context"
	"sync"
	"testing"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdvanceUpdatesKeyVC(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)

	vm.Advance("key1")
	vm.Advance("key1")
	vm.Advance("key2")

	vc1 := vm.KeyVersionClock("key1")
	require.NotNil(t, vc1)
	assert.Equal(t, uint64(2), vc1["node-a"])

	vc2 := vm.KeyVersionClock("key2")
	require.NotNil(t, vc2)
	assert.Equal(t, uint64(3), vc2["node-a"])
}

func TestHandleUpdateUpdatesKeyVC(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	counter := crdt.NewPNCounter("remote")
	delta := counter.Increment(5)

	update := &types.Update{
		NodeID:       "remote",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
		Payload:      delta,
	}

	applied := vm.Update(ctx, update)
	require.Len(t, applied, 1)

	vc := vm.KeyVersionClock("counter:1")
	require.NotNil(t, vc)
	assert.Equal(t, uint64(1), vc["remote"])
}

func TestKeyDigests(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)

	vm.Advance("key1")
	vm.Advance("key2")
	vm.Advance("key3")

	// Collect digests from all buckets
	allDigests := make(map[string]uint64)
	for b := uint32(0); b < vm.NumBuckets(); b++ {
		for k, v := range vm.KeyDigests(b) {
			allDigests[k] = v
		}
	}
	assert.Len(t, allDigests, 3)

	// All hashes should be non-zero
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

func TestKeyVersionClock(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vm := NewVersionManager("node-a", eng)

	vm.Advance("mykey")

	vc := vm.KeyVersionClock("mykey")
	require.NotNil(t, vc)
	assert.Equal(t, uint64(1), vc["node-a"])

	// Non-existent key
	vc2 := vm.KeyVersionClock("nonexistent")
	assert.Nil(t, vc2)
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
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
		VC:           map[string]uint64{"remote": 5},
	}

	err = vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	entry, ok := eng.Get(ctx, "new-key")
	require.True(t, ok)
	assert.NotNil(t, entry)
	assert.Equal(t, int64(42), entry.Object.Value())

	// Check VC was merged
	vc := vm.KeyVersionClock("new-key")
	assert.Equal(t, uint64(5), vc["remote"])
}

func TestMergeKeyState_ExistingKey_MergeCRDT(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	// Create local counter with value 10
	localCounter := crdt.NewPNCounter("local")
	localCounter.Increment(10)
	eng.PutWithTimeStamp(ctx, &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "local"}, "key1", localCounter, nil)
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
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
		VC:           map[string]uint64{"remote": 3},
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
	eng.PutWithTimeStamp(ctx, &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "local"}, "key1", localCounter, nil)

	// Remote sends tombstone with newer timestamp
	remoteState, _ := crdt.NewPNCounter("remote").MarshalJSON()
	ks := &types.KeyState{
		Key:          "key1",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        remoteState,
		Tombstone:    true,
		SetTimeStamp: &hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "remote"},
		VC:           map[string]uint64{"remote": 2},
	}

	err := vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	// Entry should be deleted (tombstoned)
	_, ok := eng.Get(ctx, "key1")
	assert.False(t, ok)
}

func TestMergeKeyState_VCMerge(t *testing.T) {
	eng := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("local"))
	vm := NewVersionManager("local", eng)
	ctx := context.Background()

	// Set local key VC
	vm.Advance("key1") // local -> 1

	// Create entry in engine
	counter := crdt.NewPNCounter("local")
	eng.PutWithTimeStamp(ctx, &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "local"}, "key1", counter, nil)

	// Remote state with different VC
	remoteState, _ := crdt.NewPNCounter("remote").MarshalJSON()
	ks := &types.KeyState{
		Key:          "key1",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        remoteState,
		Tombstone:    false,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote"},
		VC:           map[string]uint64{"remote": 5, "local": 0},
	}

	err := vm.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	vc := vm.KeyVersionClock("key1")
	// Element-wise max: local=max(1,0)=1, remote=max(0,5)=5
	assert.Equal(t, uint64(1), vc["local"])
	assert.Equal(t, uint64(5), vc["remote"])
}

func TestHashVCDeterministic(t *testing.T) {
	vc := map[string]uint64{"node-a": 1, "node-b": 2, "node-c": 3}
	h1 := HashVC(vc)
	h2 := HashVC(vc)
	assert.Equal(t, h1, h2)
}

func TestHashVCDifferentForDifferentVCs(t *testing.T) {
	vc1 := map[string]uint64{"node-a": 1, "node-b": 2}
	vc2 := map[string]uint64{"node-a": 1, "node-b": 3}
	h1 := HashVC(vc1)
	h2 := HashVC(vc2)
	assert.NotEqual(t, h1, h2)
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
				vm.KeyVersionClock("key")
			}
		}()
	}

	wg.Wait()

	vc := vm.KeyVersionClock("key")
	assert.Equal(t, uint64(numGoroutines*opsPerGoroutine), vc["node-a"])
}
