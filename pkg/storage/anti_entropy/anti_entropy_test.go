package anti_entropy

import (
	"context"
	"testing"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	v2 "github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTS creates an HLC timestamp with incremented wall time for ordering.
func makeTS(wall uint64, lamport uint64, id string) hlc.Timestamp {
	return hlc.Timestamp{WallTime: wall, Lamport: lamport, ID: id}
}

// simulatedNode holds engine + version_manager + anti_entropy service for one node.
type simulatedNode struct {
	id      string
	engine  engine.Engine
	vm      *v2.VersionManager
	service *Service
	seq     uint64 // local seq counter for building updates
}

func newNode(id string) *simulatedNode {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID(id),
	)
	vm := v2.NewVersionManager(id, eng)

	return &simulatedNode{
		id:      id,
		engine:  eng,
		vm:      vm,
		service: NewService(eng, vm),
	}
}

func (n *simulatedNode) nextSeq() uint64 {
	n.seq++
	return n.seq
}

// applyLocal applies an update originating from this node.
func (n *simulatedNode) applyLocal(ctx context.Context, update types.Update) {
	update.NodeID = n.id
	update.Seq = n.nextSeq()
	n.vm.Update(ctx, update)
}

// applyRemote applies a remote update to this node.
func (n *simulatedNode) applyRemote(ctx context.Context, updates ...types.Update) {
	n.vm.Update(ctx, updates...)
}

// runAntiEntropy simulates one anti-entropy round: node pulls from peer.
// Returns the list of stale keys detected and merged keys.
func runAntiEntropy(ctx context.Context, local, remote *simulatedNode) (staleKeys []string, mergedKeys []string) {
	// Compare digests across all buckets
	for bucket := uint32(0); bucket < v2.DefaultNumBuckets; bucket++ {
		localDigests := local.vm.KeyDigests(bucket)
		remoteDigests := remote.vm.KeyDigests(bucket)

		stale := local.service.FindStaleKeys(localDigests, remoteDigests)
		staleKeys = append(staleKeys, stale...)
	}

	if len(staleKeys) == 0 {
		return staleKeys, nil
	}

	// Pull states from remote
	states := remote.service.CollectKeyStates(ctx, staleKeys)

	// Merge into local
	mergedKeys = local.service.MergeKeyStates(ctx, states)
	return staleKeys, mergedKeys
}

// TestAntiEntropy_CreateDeleteSetDeltaGap reproduces the exact user bug scenario:
// 1. Three nodes, PNCounter created on each
// 2. Delete on node1 and node3 → deleted everywhere
// 3. Set on node1 → reaches node2 but NOT node3
// 4. Delta (increment) reaches node3, revives tombstone with old CRDT state
// 5. Anti-entropy between node2 (correct) and node3 (wrong) MUST detect divergence
func TestAntiEntropy_CreateDeleteSetDeltaGap(t *testing.T) {
	ctx := context.Background()
	node1 := newNode("node1")
	node2 := newNode("node2")
	node3 := newNode("node3")

	key := "counter:test"

	// Step 1: Create counter on all nodes via Set
	// node1 originates the Set
	setDelta := &crdt.PNCounterDelta{P: map[string]int64{}, N: map[string]int64{}}
	setTS := makeTS(100, 0, "node1")
	setUpdate := types.Update{
		NodeID:       "node1",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeSet,
		TimeStamp:    setTS,
		SetTimeStamp: setTS,
		Payload:      setDelta,
	}
	node1.applyRemote(ctx, setUpdate)
	node2.applyRemote(ctx, setUpdate)
	node3.applyRemote(ctx, setUpdate)

	// Step 2: Delete on node1 (seq=2) and node3 (seq=2)
	deleteTS1 := makeTS(200, 0, "node1")
	deleteUpdate1 := types.Update{
		NodeID:       "node1",
		Seq:          2,
		Key:          key,
		Type:         types.UpdateTypeDelete,
		TimeStamp:    deleteTS1,
		SetTimeStamp: deleteTS1,
		Payload:      setDelta,
	}
	deleteTS3 := makeTS(200, 0, "node3")
	deleteUpdate3 := types.Update{
		NodeID:       "node3",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeDelete,
		TimeStamp:    deleteTS3,
		SetTimeStamp: deleteTS3,
		Payload:      setDelta,
	}

	// Apply deletes everywhere
	node1.applyRemote(ctx, deleteUpdate1)
	node2.applyRemote(ctx, deleteUpdate1)
	node3.applyRemote(ctx, deleteUpdate1)
	node1.applyRemote(ctx, deleteUpdate3)
	node2.applyRemote(ctx, deleteUpdate3)
	node3.applyRemote(ctx, deleteUpdate3)

	// Step 3: Set on node1 (seq=3) — reaches node2 but NOT node3
	newSetTS := makeTS(300, 0, "node1")
	newSetUpdate := types.Update{
		NodeID:       "node1",
		Seq:          3,
		Key:          key,
		Type:         types.UpdateTypeSet,
		TimeStamp:    newSetTS,
		SetTimeStamp: newSetTS,
		Payload:      setDelta, // empty counter
	}
	node1.applyRemote(ctx, newSetUpdate)
	node2.applyRemote(ctx, newSetUpdate)
	// node3 does NOT get this Set

	// Step 4: Increments from multiple sources on node1 — reaches all nodes
	// node1 increments its own counter
	incDelta1 := &crdt.PNCounterDelta{P: map[string]int64{"node1": 10}, N: map[string]int64{}}
	incTS1 := makeTS(400, 0, "node1")
	incUpdate1 := types.Update{
		NodeID:       "node1",
		Seq:          4,
		Key:          key,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    incTS1,
		SetTimeStamp: newSetTS,
		Payload:      incDelta1,
	}
	node1.applyRemote(ctx, incUpdate1)
	node2.applyRemote(ctx, incUpdate1)
	node3.applyRemote(ctx, incUpdate1) // This revives tombstone on node3 with only the increment

	// node2 also increments (reaches node1 and node2 only, not node3 for this specific delta)
	incDelta2 := &crdt.PNCounterDelta{P: map[string]int64{"node2": 10}, N: map[string]int64{}}
	incTS2 := makeTS(500, 0, "node2")
	incUpdate2 := types.Update{
		NodeID:       "node2",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    incTS2,
		SetTimeStamp: newSetTS,
		Payload:      incDelta2,
	}
	node1.applyRemote(ctx, incUpdate2)
	node2.applyRemote(ctx, incUpdate2)
	// node3 does NOT get node2's increment

	// Verify: node2 has value 20 (10+10), node3 has value 10 (only node1's increment)
	entry2, ok := node2.engine.Get(ctx, key)
	require.True(t, ok, "node2 should have the key")
	val2 := entry2.Object.Value().(int64)
	assert.Equal(t, int64(20), val2, "node2 should have value 20")

	entry3, ok := node3.engine.Get(ctx, key)
	require.True(t, ok, "node3 should have the key (revived by delta)")
	val3 := entry3.Object.Value().(int64)
	assert.Equal(t, int64(10), val3, "node3 should have value 10 (missing node2's increment)")
	t.Logf("node2 value: %d, node3 value: %d", val2, val3)

	// CRITICAL: node2 and node3 have different CRDT states.
	// Anti-entropy MUST detect this divergence.
	staleKeys, mergedKeys := runAntiEntropy(ctx, node3, node2)
	t.Logf("stale keys: %v, merged keys: %v", staleKeys, mergedKeys)

	assert.NotEmpty(t, staleKeys, "anti-entropy must detect divergence between node2 and node3")

	// After merge, node3 should converge to node2's value
	entry3After, ok := node3.engine.Get(ctx, key)
	require.True(t, ok)
	val3After := entry3After.Object.Value().(int64)
	assert.Equal(t, int64(20), val3After, "after anti-entropy, node3 should converge to value 20")
}

// TestAntiEntropy_SameStateNoDivergence verifies that identical nodes produce identical digests.
func TestAntiEntropy_SameStateNoDivergence(t *testing.T) {
	ctx := context.Background()
	node1 := newNode("node1")
	node2 := newNode("node2")

	key := "counter:same"

	setDelta := &crdt.PNCounterDelta{P: map[string]int64{}, N: map[string]int64{}}
	setTS := makeTS(100, 0, "node1")
	update := types.Update{
		NodeID:       "node1",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeSet,
		TimeStamp:    setTS,
		SetTimeStamp: setTS,
		Payload:      setDelta,
	}
	node1.applyRemote(ctx, update)
	node2.applyRemote(ctx, update)

	// Increment on both
	incDelta := &crdt.PNCounterDelta{P: map[string]int64{"node1": 5}, N: map[string]int64{}}
	incTS := makeTS(200, 0, "node1")
	incUpdate := types.Update{
		NodeID:       "node1",
		Seq:          2,
		Key:          key,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    incTS,
		SetTimeStamp: setTS,
		Payload:      incDelta,
	}
	node1.applyRemote(ctx, incUpdate)
	node2.applyRemote(ctx, incUpdate)

	staleKeys, _ := runAntiEntropy(ctx, node1, node2)
	assert.Empty(t, staleKeys, "identical state should produce no stale keys")
}

// TestAntiEntropy_TombstoneVsAlive verifies divergence is detected when one node has
// a tombstone and the other has an alive entry.
func TestAntiEntropy_TombstoneVsAlive(t *testing.T) {
	ctx := context.Background()
	node1 := newNode("node1")
	node2 := newNode("node2")

	key := "counter:tomb"

	// Create on both
	setDelta := &crdt.PNCounterDelta{P: map[string]int64{}, N: map[string]int64{}}
	setTS := makeTS(100, 0, "node1")
	setUpdate := types.Update{
		NodeID:       "node1",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeSet,
		TimeStamp:    setTS,
		SetTimeStamp: setTS,
		Payload:      setDelta,
	}
	node1.applyRemote(ctx, setUpdate)
	node2.applyRemote(ctx, setUpdate)

	// Delete on node1 only
	deleteTS := makeTS(200, 0, "node1")
	deleteUpdate := types.Update{
		NodeID:       "node1",
		Seq:          2,
		Key:          key,
		Type:         types.UpdateTypeDelete,
		TimeStamp:    deleteTS,
		SetTimeStamp: deleteTS,
		Payload:      setDelta,
	}
	node1.applyRemote(ctx, deleteUpdate)
	// node2 doesn't get the delete

	staleKeys, mergedKeys := runAntiEntropy(ctx, node2, node1)
	t.Logf("stale keys: %v, merged keys: %v", staleKeys, mergedKeys)
	assert.NotEmpty(t, staleKeys, "tombstone vs alive must be detected")
}

// TestAntiEntropy_DifferentIncrementValues verifies divergence when nodes have
// different PNCounter values due to missed deltas.
func TestAntiEntropy_DifferentIncrementValues(t *testing.T) {
	ctx := context.Background()
	node1 := newNode("node1")
	node2 := newNode("node2")

	key := "counter:diff"

	// Create on both
	setDelta := &crdt.PNCounterDelta{P: map[string]int64{}, N: map[string]int64{}}
	setTS := makeTS(100, 0, "node1")
	setUpdate := types.Update{
		NodeID:       "node1",
		Seq:          1,
		Key:          key,
		Type:         types.UpdateTypeSet,
		TimeStamp:    setTS,
		SetTimeStamp: setTS,
		Payload:      setDelta,
	}
	node1.applyRemote(ctx, setUpdate)
	node2.applyRemote(ctx, setUpdate)

	// Increment on node1: seq=2 (+5) — only node1 gets it
	incDelta1 := &crdt.PNCounterDelta{P: map[string]int64{"node1": 5}, N: map[string]int64{}}
	incUpdate1 := types.Update{
		NodeID:       "node1",
		Seq:          2,
		Key:          key,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    makeTS(200, 0, "node1"),
		SetTimeStamp: setTS,
		Payload:      incDelta1,
	}
	node1.applyRemote(ctx, incUpdate1)
	// node2 misses seq=2

	// Increment on node1: seq=3 (+10 total) — both nodes get it
	incDelta2 := &crdt.PNCounterDelta{P: map[string]int64{"node1": 10}, N: map[string]int64{}}
	incUpdate2 := types.Update{
		NodeID:       "node1",
		Seq:          3,
		Key:          key,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    makeTS(300, 0, "node1"),
		SetTimeStamp: setTS,
		Payload:      incDelta2,
	}
	node1.applyRemote(ctx, incUpdate2)
	node2.applyRemote(ctx, incUpdate2)

	// node1 has value 10 (max(5,10)=10), node2 has value 10 (max(0,10)=10)
	// BUT the CRDT states are actually the same because PNCounter uses max semantics!
	// This is a case where the CRDT merge would converge automatically.
	// The interesting case is when seq=2 had a *different node's* contribution.

	// Let's test a case where the gap actually matters:
	// node1: Set, then increment from "nodeA" (+5), then increment from "nodeB" (+3)
	// node2: Set, then only increment from "nodeB" (+3) — missed nodeA's increment
	nodeA := newNode("nodeA")
	nodeB := newNode("nodeB")

	key2 := "counter:gap"
	setTS2 := makeTS(100, 0, "node1")
	setUpdate2 := types.Update{
		NodeID:       "node1",
		Seq:          1,
		Key:          key2,
		Type:         types.UpdateTypeSet,
		TimeStamp:    setTS2,
		SetTimeStamp: setTS2,
		Payload:      setDelta,
	}
	_ = nodeA
	_ = nodeB
	node1.applyRemote(ctx, setUpdate2)
	node2.applyRemote(ctx, setUpdate2)

	// nodeA increment reaches node1 only
	incDeltaA := &crdt.PNCounterDelta{P: map[string]int64{"nodeA": 5}, N: map[string]int64{}}
	incUpdateA := types.Update{
		NodeID:       "nodeA",
		Seq:          1,
		Key:          key2,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    makeTS(200, 0, "nodeA"),
		SetTimeStamp: setTS2,
		Payload:      incDeltaA,
	}
	node1.applyRemote(ctx, incUpdateA)
	// node2 misses this

	// nodeB increment reaches both
	incDeltaB := &crdt.PNCounterDelta{P: map[string]int64{"nodeB": 3}, N: map[string]int64{}}
	incUpdateB := types.Update{
		NodeID:       "nodeB",
		Seq:          1,
		Key:          key2,
		Type:         types.UpdateTypeDelta,
		TimeStamp:    makeTS(300, 0, "nodeB"),
		SetTimeStamp: setTS2,
		Payload:      incDeltaB,
	}
	node1.applyRemote(ctx, incUpdateB)
	node2.applyRemote(ctx, incUpdateB)

	// node1: P={nodeA:5, nodeB:3} → value=8
	// node2: P={nodeB:3} → value=3
	entry1, ok := node1.engine.Get(ctx, key2)
	require.True(t, ok)
	assert.Equal(t, int64(8), entry1.Object.Value().(int64))

	entry2, ok := node2.engine.Get(ctx, key2)
	require.True(t, ok)
	assert.Equal(t, int64(3), entry2.Object.Value().(int64))

	// Anti-entropy must detect this
	staleKeys, mergedKeys := runAntiEntropy(ctx, node2, node1)
	t.Logf("stale keys: %v, merged keys: %v", staleKeys, mergedKeys)
	assert.NotEmpty(t, staleKeys, "different CRDT state must be detected")

	// After merge, node2 should converge
	entry2After, ok := node2.engine.Get(ctx, key2)
	require.True(t, ok)
	assert.Equal(t, int64(8), entry2After.Object.Value().(int64), "after merge, node2 should have value 8")
}
