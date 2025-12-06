package entry_updater

import (
	"errors"
	"fmt"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/crdt/hlc"
	. "in-memorydb/pkg/crdt/mocks"
	"in-memorydb/pkg/storage/engine"
	types "in-memorydb/pkg/types"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

type EntryUpdaterTestSuite struct {
	suite.Suite
	ctrl    *gomock.Controller
	fabric  *MockCRDTFabric
	updater *EntryUpdater
}

func (s *EntryUpdaterTestSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.fabric = NewMockCRDTFabric(s.ctrl)
	s.updater = NewEntryUpdater(s.fabric, "test-node")
}

func (s *EntryUpdaterTestSuite) TearDownTest() {
	s.ctrl.Finish()
}

func TestEntryUpdaterTestSuite(t *testing.T) {
	suite.Run(t, new(EntryUpdaterTestSuite))
}

func (s *EntryUpdaterTestSuite) newMockDelta(typ string) *MockDelta {
	m := NewMockDelta(s.ctrl)
	m.EXPECT().Type().Return(crdt.CRDTType(typ)).AnyTimes()
	return m
}

func (s *EntryUpdaterTestSuite) newMockCRDT(typ string) *MockCRDT {
	m := NewMockCRDT(s.ctrl)
	m.EXPECT().Type().Return(crdt.CRDTType(typ)).AnyTimes()
	return m
}

func (s *EntryUpdaterTestSuite) TestCreateFromUpdateSet() {
	update := &types.Update{
		Type:         types.UpdateTypeSet,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Payload:      s.newMockDelta("counter"),
	}

	newCRDT := s.newMockCRDT("counter")
	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil)

	entry, err := s.updater.CreateFromUpdate(update)

	require.NoError(s.T(), err)
	require.NotNil(s.T(), entry)
	assert.Equal(s.T(), newCRDT, entry.Object)
	assert.Equal(s.T(), uint64(100), entry.SetTimeStamp.WallTime)
	assert.False(s.T(), entry.Tombstone)
}

func (s *EntryUpdaterTestSuite) TestCreateFromUpdateDelta() {
	delta := s.newMockDelta("counter")
	update := &types.Update{
		Type:         types.UpdateTypeDelta,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Payload:      delta,
	}

	newCRDT := s.newMockCRDT("counter")
	newCRDT.EXPECT().ApplyDelta(delta).Return(nil)

	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil)

	entry, err := s.updater.CreateFromUpdate(update)

	require.NoError(s.T(), err)
	require.NotNil(s.T(), entry)
}

func (s *EntryUpdaterTestSuite) TestCreateFromUpdateError() {
	update := &types.Update{
		Type:         types.UpdateTypeSet,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100},
		Payload:      s.newMockDelta("unk"),
	}

	s.fabric.EXPECT().
		New(crdt.CRDTType("unk"), "test-node").
		Return(nil, errors.New("unknown type"))

	entry, err := s.updater.CreateFromUpdate(update)

	assert.Error(s.T(), err)
	assert.Nil(s.T(), entry)
	assert.ErrorIs(s.T(), err, ErrCreateCRDT)
}

// === ApplyUpdate - Set ===

func (s *EntryUpdaterTestSuite) TestApplyUpdateSet() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		Tombstone:    false,
	}

	update := &types.Update{
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"},
		Payload:      s.newMockDelta("counter"),
	}

	newCRDT := s.newMockCRDT("counter")
	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil)

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.True(s.T(), result.Modified)
	assert.NoError(s.T(), result.Error)
	assert.Equal(s.T(), newCRDT, entry.Object)
	assert.False(s.T(), entry.Tombstone)
}

func (s *EntryUpdaterTestSuite) TestApplyUpdateSetOldTimestamp() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "new"},
		Tombstone:    false,
	}

	update := &types.Update{
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "old"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "old"},
		Payload:      s.newMockDelta("counter"),
	}

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.False(s.T(), result.Modified)
}

// === ApplyUpdate - Delta ===

func (s *EntryUpdaterTestSuite) TestApplyUpdateDeltaSameType() {
	existingCRDT := s.newMockCRDT("counter")
	entry := &engine.CRDTEntry{
		Object:       existingCRDT,
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	delta := s.newMockDelta("counter")
	update := &types.Update{
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 60, Lamport: 0, ID: "remote"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same
		Payload:      delta,
	}

	existingCRDT.EXPECT().ApplyDelta(delta).Return(nil)

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.True(s.T(), result.Modified)
	assert.NoError(s.T(), result.Error)
}

func (s *EntryUpdaterTestSuite) TestApplyUpdateDeltaDifferentTypeNewerSetTS() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		Tombstone:    false,
	}

	newCRDT := s.newMockCRDT("register")
	update := &types.Update{
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"}, // Newer!
		Payload:      s.newMockDelta("register"),
	}

	// Должен создать новый CRDT (как Set)
	s.fabric.EXPECT().
		New(crdt.CRDTType("register"), "test-node").
		Return(newCRDT, nil)

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.True(s.T(), result.Modified)
	assert.NoError(s.T(), result.Error)
	assert.Equal(s.T(), newCRDT, entry.Object)
}

func (s *EntryUpdaterTestSuite) TestApplyUpdateDeltaDifferentTypeSameSetTS() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	update := &types.Update{
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 60, Lamport: 0, ID: "remote"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same
		Payload:      s.newMockDelta("register"),                           // Different type!
	}

	// this update should be ignored since it has the same SetTimeStamp

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.False(s.T(), result.Modified)
}

// === ApplyUpdate - Delete ===

func (s *EntryUpdaterTestSuite) TestApplyUpdateDelete() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		Tombstone:    false,
	}

	update := &types.Update{
		Type:         types.UpdateTypeDelete,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "new"},
	}

	result := s.updater.ApplyUpdate(entry, update)

	assert.True(s.T(), result.Applied)
	assert.True(s.T(), result.Modified)
	assert.NoError(s.T(), result.Error)
	assert.True(s.T(), entry.Tombstone)
	assert.Equal(s.T(), uint64(100), entry.SetTimeStamp.WallTime)
}

// === Unknown update type ===

func (s *EntryUpdaterTestSuite) TestApplyUpdateUnknownType() {
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50},
		Tombstone:    false,
	}

	update := &types.Update{
		Type:      types.UpdateType("unk"), // Unknown type
		TimeStamp: &hlc.Timestamp{WallTime: 100},
	}

	result := s.updater.ApplyUpdate(entry, update)

	assert.False(s.T(), result.Applied)
	assert.False(s.T(), result.Modified)
	assert.Error(s.T(), result.Error)
}

// === Integration Tests (реальные CRDT) ===

func TestEntryUpdaterIntegrationGCounter(t *testing.T) {
	fabric := crdt.NewFabric()
	updater := NewEntryUpdater(fabric, "test-node")

	// Create entry from Set update
	update1 := &types.Update{
		Type:         types.UpdateTypeSet,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Payload:      crdt.NewPNCounter("test-node").Increment(1),
	}

	entry, err := updater.CreateFromUpdate(update1)
	require.NoError(t, err)
	require.NotNil(t, entry)

	counter, ok := entry.Object.(*crdt.PNCounter)
	require.True(t, ok)

	// Apply delta update
	counter.Increment(5)
	delta := crdt.NewPNCounter("remote-node").Increment(5)

	update2 := &types.Update{
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Payload:      delta,
	}

	result := updater.ApplyUpdate(entry, update2)
	assert.True(t, result.Applied)
	assert.NoError(t, result.Error)
	assert.Equal(t, entry.Object.Value(), int64(10))
}

func TestEntryUpdaterTimestampOrdering(t *testing.T) {
	fabric := crdt.NewFabric()
	updater := NewEntryUpdater(fabric, "test-node")

	// Create initial entry
	counter := crdt.NewPNCounter("test-node")
	entry := &engine.CRDTEntry{
		Object:       counter,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	// Try to apply update with older timestamp
	oldUpdate := &types.Update{
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		Payload:      &crdt.PNCounterDelta{},
	}

	result := updater.ApplyUpdate(entry, oldUpdate)
	assert.True(t, result.Applied)
	assert.False(t, result.Modified)
	assert.Equal(t, entry.Object, counter)

	// Apply update with newer timestamp
	newCounter := crdt.NewPNCounter("test-node")
	newUpdate := &types.Update{
		NodeID:       "test-node",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "new"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old"},
		Payload:      newCounter.Increment(10),
	}

	result = updater.ApplyUpdate(entry, newUpdate)
	assert.True(t, result.Applied)
	assert.NoError(t, result.Error)
	assert.Equal(t, int64(10), entry.Object.Value())
}

func TestEntryUpdaterConcurrentDeltaApplications(t *testing.T) {
	fabric := crdt.NewFabric()
	updater := NewEntryUpdater(fabric, "test-node")

	counter := crdt.NewPNCounter("test-node")
	entry := &engine.CRDTEntry{
		Object:       counter,
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	// Apply multiple deltas
	for i := 0; i < 10; i++ {
		c := crdt.NewPNCounter("node-" + strconv.Itoa(i))
		delta := c.Increment(int64(i + 1))

		update := &types.Update{
			Type:         types.UpdateTypeDelta,
			TimeStamp:    &hlc.Timestamp{WallTime: uint64(110 + i), Lamport: 0, ID: fmt.Sprintf("node-%d", i)},
			SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "node"},
			Payload:      delta,
		}

		result := updater.ApplyUpdate(entry, update)
		assert.True(t, result.Applied)
		assert.NoError(t, result.Error)
	}

	// Verify all deltas were applied
	value := counter.Value()
	expected := int64(0)
	for i := 0; i < 10; i++ {
		expected += int64(i + 1)
	}
	assert.Equal(t, expected, value)
}
