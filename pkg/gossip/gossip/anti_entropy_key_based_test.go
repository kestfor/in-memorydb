package gossip

import (
	"context"
	"testing"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	vmv2 "github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2"
	mock_transport "github.com/kestfor/in-memorydb/pkg/transport/mocks"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestAntiEntropyKeyBased_PNCounterConvergence(t *testing.T) {
	ctx := context.Background()

	// --- Node A setup ---
	engA := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vmA := vmv2.NewVersionManager("node-a", engA)

	// Node A: create counter and increment to 10
	counterA := crdt.NewPNCounter("node-a")
	counterA.Increment(10)
	engA.PutWithTimeStamp(ctx, hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node-a"}, "counter:x", counterA, nil)
	vmA.Advance("counter:x")

	// --- Node B setup ---
	engB := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-b"))
	vmB := vmv2.NewVersionManager("node-b", engB)

	// Node B: create counter and increment to 20
	counterB := crdt.NewPNCounter("node-b")
	counterB.Increment(20)
	engB.PutWithTimeStamp(ctx, hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node-b"}, "counter:x", counterB, nil)
	vmB.Advance("counter:x")

	// --- Simulate anti-entropy: B pulls from A ---
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockTransport := mock_transport.NewMockTransport(ctrl)

	// Step 1: B gets A's key digests (collect all buckets)
	digestsA := allKeyDigests(vmA)

	// Step 2: B compares with local digests
	digestsB := allKeyDigests(vmB)

	var staleKeys []string
	for key, remoteHash := range digestsA {
		localHash, exists := digestsB[key]
		if !exists || localHash != remoteHash {
			staleKeys = append(staleKeys, key)
		}
	}
	require.Len(t, staleKeys, 1)
	assert.Equal(t, "counter:x", staleKeys[0])

	// Step 3: B pulls key state from A
	entryA, ok := engA.Get(ctx, "counter:x")
	require.True(t, ok)

	entryA.Mu.RLock()
	stateBytes, err := entryA.Object.MarshalJSON()
	entryA.Mu.RUnlock()
	require.NoError(t, err)

	keyStateFromA := &types.KeyState{
		Key:          "counter:x",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        stateBytes,
		Tombstone:    false,
		SetTimeStamp: entryA.SetTimeStamp,
		VC:           vmA.KeyVersionClock("counter:x"),
	}

	// Step 4: B merges A's state
	err = vmB.MergeKeyState(ctx, keyStateFromA)
	require.NoError(t, err)

	// Verify convergence: B should have merged counter = max(10,0) + max(0,20) = 30
	entryB, ok := engB.Get(ctx, "counter:x")
	require.True(t, ok)
	assert.Equal(t, int64(30), entryB.Object.Value())

	// Verify B's VC was updated
	vcB := vmB.KeyVersionClock("counter:x")
	assert.Equal(t, uint64(1), vcB["node-a"])
	assert.Equal(t, uint64(1), vcB["node-b"])

	_ = mockTransport // referenced to prevent unused import
}

func TestAntiEntropyKeyBased_NewKeyFromRemote(t *testing.T) {
	ctx := context.Background()

	// Node A has a key, Node B doesn't
	engA := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-a"))
	vmA := vmv2.NewVersionManager("node-a", engA)

	counter := crdt.NewPNCounter("node-a")
	counter.Increment(42)
	engA.PutWithTimeStamp(ctx, hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node-a"}, "new-key", counter, nil)
	vmA.Advance("new-key")

	// Node B is empty
	engB := enginev1.NewEngine(enginev1.WithInitialShards(4), enginev1.WithNodeID("node-b"))
	vmB := vmv2.NewVersionManager("node-b", engB)

	// B's local digests are empty
	digestsB := allKeyDigests(vmB)
	assert.Len(t, digestsB, 0)

	// B sees A has "new-key"
	digestsA := allKeyDigests(vmA)
	require.Contains(t, digestsA, "new-key")

	// B pulls state for "new-key"
	entryA, ok := engA.Get(ctx, "new-key")
	require.True(t, ok)

	entryA.Mu.RLock()
	stateBytes, _ := entryA.Object.MarshalJSON()
	entryA.Mu.RUnlock()

	ks := &types.KeyState{
		Key:          "new-key",
		CRDTType:     crdt.CRDTTypePNCounter,
		State:        stateBytes,
		Tombstone:    false,
		SetTimeStamp: entryA.SetTimeStamp,
		VC:           vmA.KeyVersionClock("new-key"),
	}

	err := vmB.MergeKeyState(ctx, ks)
	require.NoError(t, err)

	// B should now have the key
	entryB, ok := engB.Get(ctx, "new-key")
	require.True(t, ok)
	assert.Equal(t, int64(42), entryB.Object.Value())
}

// allKeyDigests collects key digests from all buckets
func allKeyDigests(vm *vmv2.VersionManager) map[string]uint64 {
	result := make(map[string]uint64)
	for b := uint32(0); b < vm.NumBuckets(); b++ {
		for k, v := range vm.KeyDigests(b) {
			result[k] = v
		}
	}
	return result
}
