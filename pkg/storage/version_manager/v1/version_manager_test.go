package v1

import (
	"context"
	"errors"
	"github/kestfor/in-memorydb/pkg/crdt"
	"github/kestfor/in-memorydb/pkg/crdt/hlc"
	crdtmock "github/kestfor/in-memorydb/pkg/crdt/mocks"
	"github/kestfor/in-memorydb/pkg/storage/engine"
	enginemock "github/kestfor/in-memorydb/pkg/storage/engine/mocks"
	enginev1 "github/kestfor/in-memorydb/pkg/storage/engine/v1"
	"github/kestfor/in-memorydb/pkg/storage/version_manager/v1/entry_updater"
	"github/kestfor/in-memorydb/pkg/storage/version_manager/v1/history"
	"github/kestfor/in-memorydb/pkg/structs"
	types "github/kestfor/in-memorydb/pkg/types"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

// Test Suite
type VersionManagerTestSuite struct {
	suite.Suite
	ctrl    *gomock.Controller
	engine  *enginemock.MockEngine
	history *history.History
	fabric  *crdtmock.MockCRDTFabric
	vm      *VersionManager
	ctx     context.Context
}

func (s *VersionManagerTestSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.engine = enginemock.NewMockEngine(s.ctrl)
	s.history = history.NewHistory()
	s.fabric = crdtmock.NewMockCRDTFabric(s.ctrl)
	s.ctx = context.Background()

	// Создаём VM с моками
	s.vm = &VersionManager{
		nodeID:  "test-node",
		history: s.history,
		engine:  s.engine,
		updater: entry_updater.NewEntryUpdater(s.fabric, "test-node"),
	}
}

func (s *VersionManagerTestSuite) TearDownTest() {
	s.ctrl.Finish()
}

func TestVersionManagerTestSuite(t *testing.T) {
	suite.Run(t, new(VersionManagerTestSuite))
}

func (s *VersionManagerTestSuite) TestGetCurrentSequence() {
	s.vm.seq.Store(42)
	seq := s.vm.GetCurrentSequence()
	s.Equal(uint64(42), seq)
}

func (s *VersionManagerTestSuite) TestGetVersionLocalNode() {
	s.vm.seq.Store(100)
	rng := s.vm.getVersion("test-node")
	s.Equal(uint64(100), rng.End)
}

func (s *VersionManagerTestSuite) TestGetVersionRemoteNode() {
	s.history.AddRange("remote-node", structs.Range{End: 50})
	rng := s.vm.getVersion("remote-node")
	s.Equal(uint64(50), rng.End)
}

func (s *VersionManagerTestSuite) newMockDelta(typ string) *crdtmock.MockDelta {
	m := crdtmock.NewMockDelta(s.ctrl)
	m.EXPECT().Type().Return(crdt.CRDTType(typ)).AnyTimes()
	return m
}

func (s *VersionManagerTestSuite) newMockCRDT(typ string) *crdtmock.MockCRDT {
	m := crdtmock.NewMockCRDT(s.ctrl)
	m.EXPECT().Type().Return(crdt.CRDTType(typ)).AnyTimes()
	return m
}

func (s *VersionManagerTestSuite) TestUpdateSetNewKey() {
	delta := s.newMockDelta("counter")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "new-key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "new-key", gomock.Any()).Return(false, nil)

	// Создаём новый CRDT из delta
	newCRDT := s.newMockCRDT("counter")
	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil).
		Times(1)

	// Записываем в engine
	s.engine.EXPECT().
		PutWithTimeStamp(s.ctx, gomock.Any(), "new-key", newCRDT, nil).
		Return(update.SetTimeStamp).
		Times(1)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
	s.Equal(update, applied[0])
}

func (s *VersionManagerTestSuite) TestUpdateDeltaNewKey() {
	delta := s.newMockDelta("counter")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "new-key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "new-key", gomock.Any()).Return(false, nil)

	// Создаём CRDT и применяем delta
	newCRDT := s.newMockCRDT("counter")
	newCRDT.EXPECT().ApplyDelta(delta).Return(nil)

	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil)

	s.engine.EXPECT().
		PutWithTimeStamp(s.ctx, gomock.Any(), "new-key", newCRDT, nil).
		Return(update.SetTimeStamp)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
}

// === Update: Set на существующий ключ ===

func (s *VersionManagerTestSuite) TestUpdateSetExistingKey() {
	oldCRDT := s.newMockCRDT("counter")
	oldEntry := &engine.CRDTEntry{
		Object:       oldCRDT,
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old-node"},
		Tombstone:    false,
	}

	delta := s.newMockDelta("counter")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "existing-key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "existing-key", gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			return updCall(ctx, oldEntry)
		})

	newCRDT := s.newMockCRDT("counter")
	s.fabric.EXPECT().
		New(crdt.CRDTType("counter"), "test-node").
		Return(newCRDT, nil)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)

	// Проверяем что entry обновлён
	s.Equal(newCRDT, oldEntry.Object)
	s.Equal(update.SetTimeStamp.WallTime, oldEntry.SetTimeStamp.WallTime)
	s.False(oldEntry.Tombstone)
}

func (s *VersionManagerTestSuite) TestUpdateDeltaExistingKey() {
	existingCRDT := s.newMockCRDT("counter")
	entry := &engine.CRDTEntry{
		Object:       existingCRDT,
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	delta := s.newMockDelta("counter")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "existing-key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 60, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same SetTS
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "existing-key", gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			return updCall(ctx, entry)
		})

	existingCRDT.EXPECT().ApplyDelta(delta).Return(nil)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
}

func (s *VersionManagerTestSuite) TestUpdateDelete() {
	existingCRDT := s.newMockCRDT("counter")
	entry := &engine.CRDTEntry{
		Object:       existingCRDT,
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key-to-delete",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
	}

	s.engine.EXPECT().Update(gomock.Any(), "key-to-delete", gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			return updCall(ctx, entry)
		})

	s.engine.EXPECT().
		DeleteWithTimeStamp(gomock.Any(), update.SetTimeStamp, "key-to-delete").
		Return(nil, true)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
	assert.True(s.T(), entry.Tombstone)
}

// === Конфликты и resolution ===

func (s *VersionManagerTestSuite) TestUpdateOldTimestamp() {
	// Существующий entry с более новым timestamp
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	// Update со старым timestamp
	delta := s.newMockDelta("counter")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "key", gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			return updCall(ctx, entry)
		})

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
}

func (s *VersionManagerTestSuite) TestUpdateTypeMismatch() {
	// Entry с типом GCounter
	entry := &engine.CRDTEntry{
		Object:       s.newMockCRDT("counter"),
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	// Delta update с другим типом
	delta := s.newMockDelta("register") // Другой тип!
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same SetTS
		Payload:      delta,
	}

	s.engine.EXPECT().Update(gomock.Any(), "key", gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			return updCall(ctx, entry)
		})

	applied := s.vm.Update(s.ctx, update)

	s.Len(applied, 1)
}

func (s *VersionManagerTestSuite) mockEngineUpdate(key string, entry *engine.CRDTEntry) {
	s.engine.EXPECT().Update(gomock.Any(), key, gomock.Any()).
		DoAndReturn(func(ctx context.Context, key string, updCall engine.UpdateFunc) (bool, error) {
			if entry == nil {
				return false, nil
			}
			return updCall(ctx, entry)
		})
}

func (s *VersionManagerTestSuite) TestUpdateTypeMismatchWithNewerSetTS() {
	// Entry с типом GCounter
	oldCRDT := s.newMockCRDT("counter")
	entry := &engine.CRDTEntry{
		Object:       oldCRDT,
		SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
		Tombstone:    false,
	}

	// Delta update с другим типом но более новым SetTimeStamp
	delta := s.newMockDelta("register")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"}, // Newer!
		Payload:      delta,
	}

	s.mockEngineUpdate("key", entry)

	// Должен создать новый CRDT (как Set)
	newCRDT := s.newMockCRDT("register")
	newCRDT.EXPECT().ApplyDelta(delta).Return(nil)
	s.fabric.EXPECT().New(crdt.CRDTType("register"), "test-node").Return(newCRDT, nil)

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 1)
	s.Equal(newCRDT, entry.Object)
}

// === Дубликаты ===

func (s *VersionManagerTestSuite) TestUpdateDuplicate() {
	delta := s.newMockDelta("register")
	update := &types.Update{
		NodeID:  "remote-node",
		Range:   structs.Range{Start: 1, End: 1},
		Key:     "key",
		Type:    types.UpdateTypeSet,
		Payload: delta,
	}

	s.history.Add("remote-node", 1)
	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 0)
}

// === Batch updates ===

func (s *VersionManagerTestSuite) TestUpdateBatch() {
	delta1 := s.newMockDelta("counter")
	delta2 := s.newMockDelta("register")

	updates := []*types.Update{
		{
			NodeID:       "remote-node",
			Range:        structs.Range{Start: 1, End: 1},
			Key:          "key1",
			Type:         types.UpdateTypeSet,
			TimeStamp:    &hlc.Timestamp{WallTime: 100},
			SetTimeStamp: &hlc.Timestamp{WallTime: 100},
			Payload:      delta1,
		},
		{
			NodeID:       "remote-node",
			Range:        structs.Range{Start: 2, End: 2},
			Key:          "key2",
			Type:         types.UpdateTypeSet,
			TimeStamp:    &hlc.Timestamp{WallTime: 101},
			SetTimeStamp: &hlc.Timestamp{WallTime: 101},
			Payload:      delta2,
		},
	}

	s.mockEngineUpdate("key1", nil)
	s.fabric.EXPECT().New(crdt.CRDTType("counter"), "test-node").Return(s.newMockCRDT("counter"), nil)
	s.engine.EXPECT().PutWithTimeStamp(gomock.Any(), gomock.Any(), "key1", gomock.Any(), nil).
		Return(updates[0].SetTimeStamp)

	s.mockEngineUpdate("key2", nil)
	s.fabric.EXPECT().New(crdt.CRDTType("register"), "test-node").Return(s.newMockCRDT("register"), nil)
	s.engine.EXPECT().PutWithTimeStamp(gomock.Any(), gomock.Any(), "key2", gomock.Any(), nil).
		Return(updates[1].SetTimeStamp)

	applied := s.vm.Update(s.ctx, updates...)
	s.Len(applied, 2)
	s.True(s.history.HasRange("remote-node", structs.Range{Start: 1, End: 2}))
}

// === Error handling ===

func (s *VersionManagerTestSuite) TestUpdateCRDTCreationError() {
	delta := s.newMockDelta("register")
	update := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100},
		Payload:      delta,
	}

	s.mockEngineUpdate("key", nil)

	// Ошибка при создании CRDT
	s.fabric.EXPECT().
		New(crdt.CRDTType("register"), "test-node").
		Return(nil, errors.New("unknown CRDT type"))

	applied := s.vm.Update(s.ctx, update)
	s.Len(applied, 0)
}

// === Integration Tests (без моков) ===

func TestVersionManagerIntegration(t *testing.T) {
	// Используем реальные имплементации
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	counter := crdt.NewPNCounter("test-node")
	counterDelta := counter.Increment(1)

	update1 := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      counterDelta,
	}

	applied := vm.Update(ctx, update1)
	assert.Len(t, applied, 1)

	// Verify entry exists
	entry, ok := eng.Get(ctx, "counter:1")
	require.True(t, ok)
	assert.NotNil(t, entry)
	assert.False(t, entry.Tombstone)

	// Test 2: Delta update
	counter2 := crdt.NewPNCounter("remote-node")
	delta2 := counter2.Increment(2)

	update2 := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 2, End: 2},
		Key:          "counter:1",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta2,
	}

	applied = vm.Update(ctx, update2)
	assert.Len(t, applied, 1)

	// Test 3: Delete
	update3 := &types.Update{
		NodeID:       "remote-node",
		Range:        structs.Range{Start: 3, End: 3},
		Key:          "counter:1",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    &hlc.Timestamp{WallTime: 120, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 120, Lamport: 0, ID: "remote-node"},
	}

	applied = vm.Update(ctx, update3)
	assert.Len(t, applied, 1)

	// Verify deleted
	entry, ok = eng.Get(ctx, "counter:1")
	assert.False(t, ok)  // marked as delete
	assert.Nil(t, entry) // marked as delete
}

// TODO добавить тест со сложным кофликт резолвингом
func TestVersionManagerIntegrationComplex(t *testing.T) {
	// Используем реальные имплементации
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
		enginev1.WithDeleteThreshold(time.Millisecond*500),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// локальные изменения
	counter := crdt.NewPNCounter("test-node")
	localTs1 := &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "test-node"}
	vm.engine.PutWithTimeStamp(ctx, localTs1, "key", counter, nil)
	vm.Advance()

	counter.Increment(1)
	vm.Advance()

	assert.True(t, vm.history.HasRange("test-node", structs.Range{Start: 1, End: 2}))

	// сценарий: нода А создает счетчик, инкрементит его, параллельно нода B создает регистр и удаляет ключ, должно выиграть удаление так как оно произошло после всех операций

	upd1 := &types.Update{
		NodeID:       "remote-node-1",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd2 := &types.Update{
		NodeID:       "remote-node-2",
		Range:        structs.Range{Start: 1, End: 1},
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    &hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-2"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-2"},
		Payload:      &crdt.LWWHLCRegisterDelta{},
	}

	counterDelta := crdt.NewPNCounter("remote-node-1").Increment(10)
	upd3 := &types.Update{
		NodeID:       "remote-node-1",
		Range:        structs.Range{Start: 2, End: 2},
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    &hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		Payload:      counterDelta,
	}

	upd4 := &types.Update{
		NodeID:       "remote-node-2",
		Range:        structs.Range{Start: 2, End: 2},
		Key:          "key",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    &hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-2"},
		SetTimeStamp: &hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-2"},
		Payload:      &crdt.LWWHLCRegisterDelta{},
	}

	// in order
	applied := vm.Update(ctx, upd1, upd2, upd3, upd4)
	assert.Len(t, applied, 4)
	entry, ok := eng.Get(ctx, "key")
	assert.False(t, ok)
	assert.Nil(t, entry)

	vm.history.Clear("remote-node-1")
	vm.history.Clear("remote-node-2")

	// random order + duplicates
	applied = vm.Update(ctx, upd3, upd2, upd3, upd4, upd1)
	assert.Len(t, applied, 4)
	entry, ok = eng.Get(ctx, "key")
	assert.False(t, ok)
	assert.Nil(t, entry)

	vm.history.Clear("remote-node-1")
	vm.history.Clear("remote-node-2")

	// reverse order
	applied = vm.Update(ctx, upd4, upd3, upd2, upd1)
	assert.Len(t, applied, 4)
	entry, ok = eng.Get(ctx, "key")
	assert.False(t, ok)
	assert.Nil(t, entry)

	vm.history.Clear("remote-node-1")
	vm.history.Clear("remote-node-2")

	require.NoError(t, eng.Start(ctx))
	time.Sleep(time.Second)
	eng.Stop()

	// without delete
	applied = vm.Update(ctx, upd1, upd2, upd3)
	assert.Len(t, applied, 3)
	entry, ok = eng.Get(ctx, "key")
	assert.True(t, ok)
	assert.NotNil(t, entry)
	assert.False(t, entry.Deleted())
	_, ok = entry.Object.(*crdt.LWWHLCRegister)
	assert.True(t, ok)
	vm.history.Clear("remote-node-1")
	vm.history.Clear("remote-node-2")

	// random order without delete
	applied = vm.Update(ctx, upd2, upd3, upd1)
	assert.Len(t, applied, 3)
	entry, ok = eng.Get(ctx, "key")
	assert.True(t, ok)
	assert.NotNil(t, entry)
	assert.False(t, entry.Deleted())
	_, ok = entry.Object.(*crdt.LWWHLCRegister)
	assert.True(t, ok)

}

//func TestVersionManagerConcurrency(t *testing.T) {
//	eng := NewEngine(
//		WithInitialShards(8),
//		WithNodeID("test-node"),
//	)
//	defer eng.Stop()
//
//	fabric := crdt.NewFabric()
//	vm := NewVersionManager("test-node", eng, fabric)
//
//	ctx := context.Background()
//	const numGoroutines = 10
//	const updatesPerGoroutine = 100
//
//	// Параллельно отправляем updates
//	done := make(chan bool, numGoroutines)
//
//	for i := 0; i < numGoroutines; i++ {
//		go func(id int) {
//			defer func() { done <- true }()
//
//			for j := 0; j < updatesPerGoroutine; j++ {
//				counter := crdt.NewGCounter("test-node")
//				delta, _ := counter.(crdt.Delta)
//
//				update := &types.Update{
//					NodeID: "remote-node",
//					Range:  structs.Range{Start: uint64(id*updatesPerGoroutine + j + 1), End: uint64(id*updatesPerGoroutine + j + 1)},
//					Key:    fmt.Sprintf("key-%d-%d", id, j),
//					Type:   types.UpdateTypeSet,
//					TimeStamp: &hlc.Timestamp{
//						WallTime: uint64(time.Now().UnixNano()),
//						Lamport:  uint64(j),
//						ID:       "remote-node",
//					},
//					SetTimeStamp: &hlc.Timestamp{
//						WallTime: uint64(time.Now().UnixNano()),
//						Lamport:  uint64(j),
//						ID:       "remote-node",
//					},
//					Payload: delta,
//				}
//
//				vm.Update(ctx, update)package
//				v1
//
//				import (
//					"context"
//				"errors"
//				"in-memorydb/pkg/crdt"
//				"in-memorydb/pkg/storage/engine"
//				"in-memorydb/pkg/structs"
//				types
//				"in-memorydb/pkg/types"
//				"testing"
//				"time"
//
//				"github.com/stretchr/testify/assert"
//				"github.com/stretchr/testify/require"
//				"github.com/stretchr/testify/suite"
//				"go.uber.org/mock/gomock"
//				)
//
//				// Mock CRDT для тестов
//				type mockCRDT struct {
//					typ   string
//					data  string
//					delta []crdt.CRDT
//				}
//
//				func(m *mockCRDT) Type()
//				string{return m.typ}
//				func(m *mockCRDT) Merge(other
//				crdt.CRDT)     {
//				}
//				func(m *mockCRDT) ApplyDelta(d
//				crdt.CRDT) error{
//					m.delta = append(m.delta, d)
//					return nil
//				}
//
//				// Test Suite
//				type VersionManagerTestSuite struct {
//					suite.Suite
//					ctrl    *gomock.Controller
//					engine  *MockEngine
//					history *MockHistory
//					fabric  *MockCRDTFabric
//					vm      *VersionManager
//					ctx     context.Context
//				}
//
//				func(s *VersionManagerTestSuite) SetupTest()
//				{
//					s.ctrl = gomock.NewController(s.T())
//					s.engine = NewMockEngine(s.ctrl)
//					s.history = NewMockHistory(s.ctrl)
//					s.fabric = NewMockCRDTFabric(s.ctrl)
//					s.ctx = context.Background()
//
//					// Создаём VM с моками
//					s.vm = &VersionManager{
//						nodeID:  "test-node",
//						history: s.history,
//						engine:  s.engine,
//						updater: entry_updater.NewEntryUpdater(s.fabric, "test-node"),
//					}
//				}
//
//				func(s *VersionManagerTestSuite) TearDownTest()
//				{
//					s.ctrl.Finish()
//				}
//
//				func
//				TestVersionManagerTestSuite(t * testing.T)
//				{
//					suite.Run(t, new(VersionManagerTestSuite))
//				}
//
//				// === Базовые операции ===
//
//				func(s *VersionManagerTestSuite) TestAdvance()
//				{
//					s.history.EXPECT().
//						Add("test-node", uint64(1)).
//						Times(1)
//
//					seq := s.vm.Advance()
//					s.Equal(uint64(1), seq)
//
//					s.history.EXPECT().
//						Add("test-node", uint64(2)).
//						Times(1)
//
//					seq = s.vm.Advance()
//					s.Equal(uint64(2), seq)
//				}
//
//				func(s *VersionManagerTestSuite) TestGetCurrentSequence()
//				{
//					s.vm.seq.Store(42)
//					seq := s.vm.GetCurrentSequence()
//					s.Equal(uint64(42), seq)
//				}
//
//				func(s *VersionManagerTestSuite) TestGetVersionLocalNode()
//				{
//					s.vm.seq.Store(100)
//
//					rng := s.vm.GetVersion("test-node")
//					s.Equal(uint64(100), rng.End)
//				}
//
//				func(s *VersionManagerTestSuite) TestGetVersionRemoteNode()
//				{
//					vc := types.VectorClock{"remote-node": 50}
//
//					s.history.EXPECT().
//						VectorClockContiguous().
//						Return(vc).
//						Times(1)
//
//					rng := s.vm.GetVersion("remote-node")
//					s.Equal(uint64(50), rng.End)
//				}
//
//				// === Update: Set операция на новый ключ ===
//
//				func(s *VersionManagerTestSuite) TestUpdateSetNewKey()
//				{
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "new-key",
//						Type:         types.UpdateTypeSet,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      &mockCRDT{typ: "counter", data: "42"},
//					}
//
//					// Проверяем что update не был применён ранее
//					s.history.EXPECT().
//						HasRange("remote-node", update.Range).
//						Return(false).
//						Times(1)
//
//					// Добавляем в историю
//					s.history.EXPECT().
//						AddRange("remote-node", update.Range).
//						Times(1)
//
//					// Ключа нет в engine
//					s.engine.EXPECT().
//						Get(s.ctx, "new-key").
//						Return(nil, false).
//						Times(1)
//
//					// Создаём новый CRDT
//					newCRDT := &mockCRDT{typ: "counter", data: "42"}
//					s.fabric.EXPECT().
//						New("counter", "test-node").
//						Return(newCRDT, nil).
//						Times(1)
//
//					// Записываем в engine
//					s.engine.EXPECT().
//						PutWithTimeStamp(s.ctx, gomock.Any(), "new-key", newCRDT, nil).
//						Return(update.SetTimeStamp).
//						Times(1)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//					s.Equal(update, applied[0])
//				}
//
//				// === Update: Delta операция на новый ключ ===
//
//				func(s *VersionManagerTestSuite) TestUpdateDeltaNewKey()
//				{
//					delta := &mockCRDT{typ: "counter", data: "delta"}
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "new-key",
//						Type:         types.UpdateTypeDelta,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      delta,
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//
//					s.engine.EXPECT().Get(s.ctx, "new-key").Return(nil, false)
//
//					// Создаём CRDT и применяем delta
//					newCRDT := &mockCRDT{typ: "counter"}
//					s.fabric.EXPECT().
//						New("counter", "test-node").
//						Return(newCRDT, nil)
//
//					s.engine.EXPECT().
//						PutWithTimeStamp(s.ctx, gomock.Any(), "new-key", newCRDT, nil).
//						Return(update.SetTimeStamp)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//
//					// Проверяем что delta была применена
//					s.Len(newCRDT.delta, 1)
//					s.Equal(delta, newCRDT.delta[0])
//				}
//
//				// === Update: Set на существующий ключ ===
//
//				func(s *VersionManagerTestSuite) TestUpdateSetExistingKey()
//				{
//					oldCRDT := &mockCRDT{typ: "counter", data: "old"}
//					oldEntry := &engine.CRDTEntry{
//						Object:       oldCRDT,
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "old-node"},
//						Tombstone:    false,
//					}
//
//					newCRDT := &mockCRDT{typ: "counter", data: "new"}
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "existing-key",
//						Type:         types.UpdateTypeSet,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      newCRDT,
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//
//					s.engine.EXPECT().
//						Get(s.ctx, "existing-key").
//						Return(oldEntry, true)
//
//					s.fabric.EXPECT().
//						New("counter", "test-node").
//						Return(newCRDT, nil)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//
//					// Проверяем что entry обновлён
//					s.Equal(newCRDT, oldEntry.Object)
//					s.Equal(update.SetTimeStamp.WallTime, oldEntry.SetTimeStamp.WallTime)
//					s.False(oldEntry.Tombstone)
//				}
//
//				// === Update: Delta на существующий ключ ===
//
//				func(s *VersionManagerTestSuite) TestUpdateDeltaExistingKey()
//				{
//					existingCRDT := &mockCRDT{typ: "counter", data: "42"}
//					entry := &engine.CRDTEntry{
//						Object:       existingCRDT,
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
//						Tombstone:    false,
//					}
//
//					delta := &mockCRDT{typ: "counter", data: "delta"}
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "existing-key",
//						Type:         types.UpdateTypeDelta,
//						TimeStamp:    &hlc.Timestamp{WallTime: 60, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same SetTS
//						Payload:      delta,
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//
//					s.engine.EXPECT().Get(s.ctx, "existing-key").Return(entry, true)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//
//					// Проверяем что delta применена
//					s.Len(existingCRDT.delta, 1)
//					s.Equal(delta, existingCRDT.delta[0])
//				}
//
//				// === Update: Delete ===
//
//				func(s *VersionManagerTestSuite) TestUpdateDelete()
//				{
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "key-to-delete",
//						Type:         types.UpdateTypeDelete,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//
//					s.engine.EXPECT().
//						DeleteWithTimeStamp(s.ctx, update.SetTimeStamp, "key-to-delete").
//						Return(true)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//				}
//
//				// === Конфликты и resolution ===
//
//				func(s *VersionManagerTestSuite) TestUpdateOldTimestamp()
//				{
//					// Существующий entry с более новым timestamp
//					entry := &engine.CRDTEntry{
//						Object:       &mockCRDT{typ: "counter"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 200, Lamport: 0, ID: "node"},
//						Tombstone:    false,
//					}
//
//					// Update со старым timestamp
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "key",
//						Type:         types.UpdateTypeSet,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      &mockCRDT{typ: "counter"},
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//					s.engine.EXPECT().Get(s.ctx, "key").Return(entry, true)
//
//					applied := s.vm.Update(s.ctx, update)
//
//					// Update не должен быть применён из-за старого timestamp
//					s.Len(applied, 0)
//				}
//
//				func(s *VersionManagerTestSuite) TestUpdateTypeMismatch()
//				{
//					// Entry с типом "counter"
//					entry := &engine.CRDTEntry{
//						Object:       &mockCRDT{typ: "counter"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
//						Tombstone:    false,
//					}
//
//					// Delta update с другим типом
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "key",
//						Type:         types.UpdateTypeDelta,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"}, // Same SetTS
//						Payload:      &mockCRDT{typ: "register"},                           // Другой тип!
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//					s.engine.EXPECT().Get(s.ctx, "key").Return(entry, true)
//
//					applied := s.vm.Update(s.ctx, update)
//
//					// Delta не применится из-за несовместимости типов
//					s.Len(applied, 0)
//				}
//
//				func(s *VersionManagerTestSuite) TestUpdateTypeMismatchWithNewerSetTS()
//				{
//					// Entry с типом "counter"
//					oldCRDT := &mockCRDT{typ: "counter"}
//					entry := &engine.CRDTEntry{
//						Object:       oldCRDT,
//						SetTimeStamp: &hlc.Timestamp{WallTime: 50, Lamport: 0, ID: "node"},
//						Tombstone:    false,
//					}
//
//					// Delta update с другим типом но более новым SetTimeStamp
//					newCRDT := &mockCRDT{typ: "register"}
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "key",
//						Type:         types.UpdateTypeDelta,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"}, // Newer!
//						Payload:      newCRDT,
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//					s.engine.EXPECT().Get(s.ctx, "key").Return(entry, true)
//
//					// Должен создать новый CRDT (как Set)
//					s.fabric.EXPECT().New("register", "test-node").Return(newCRDT, nil)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 1)
//
//					// Entry должен иметь новый объект
//					s.Equal(newCRDT, entry.Object)
//				}
//
//				// === Дубликаты ===
//
//				func(s *VersionManagerTestSuite) TestUpdateDuplicate()
//				{
//					update := &types.Update{
//						NodeID: "remote-node",
//						Range:  structs.Range{Start: 1, End: 1},
//						Key:    "key",
//						Type:   types.UpdateTypeSet,
//					}
//
//					// Update уже был применён
//					s.history.EXPECT().
//						HasRange("remote-node", update.Range).
//						Return(true).
//						Times(1)
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 0)
//				}
//
//				// === Batch updates ===
//
//				func(s *VersionManagerTestSuite) TestUpdateBatch()
//				{
//					updates := []*types.Update{
//						{
//							NodeID:       "remote-node",
//							Range:        structs.Range{Start: 1, End: 1},
//							Key:          "key1",
//							Type:         types.UpdateTypeSet,
//							TimeStamp:    &hlc.Timestamp{WallTime: 100},
//							SetTimeStamp: &hlc.Timestamp{WallTime: 100},
//							Payload:      &mockCRDT{typ: "counter"},
//						},
//						{
//							NodeID:       "remote-node",
//							Range:        structs.Range{Start: 2, End: 2},
//							Key:          "key2",
//							Type:         types.UpdateTypeSet,
//							TimeStamp:    &hlc.Timestamp{WallTime: 101},
//							SetTimeStamp: &hlc.Timestamp{WallTime: 101},
//							Payload:      &mockCRDT{typ: "register"},
//						},
//					}
//
//					// Первый update
//					s.history.EXPECT().HasRange("remote-node", updates[0].Range).Return(false)
//					s.history.EXPECT().AddRange("remote-node", updates[0].Range)
//					s.engine.EXPECT().Get(s.ctx, "key1").Return(nil, false)
//					s.fabric.EXPECT().New("counter", "test-node").Return(&mockCRDT{typ: "counter"}, nil)
//					s.engine.EXPECT().PutWithTimeStamp(gomock.Any(), gomock.Any(), "key1", gomock.Any(), nil).
//						Return(updates[0].SetTimeStamp)
//
//					// Второй update
//					s.history.EXPECT().HasRange("remote-node", updates[1].Range).Return(false)
//					s.history.EXPECT().AddRange("remote-node", updates[1].Range)
//					s.engine.EXPECT().Get(s.ctx, "key2").Return(nil, false)
//					s.fabric.EXPECT().New("register", "test-node").Return(&mockCRDT{typ: "register"}, nil)
//					s.engine.EXPECT().PutWithTimeStamp(gomock.Any(), gomock.Any(), "key2", gomock.Any(), nil).
//						Return(updates[1].SetTimeStamp)
//
//					applied := s.vm.Update(s.ctx, updates...)
//					s.Len(applied, 2)
//				}
//
//				// === Error handling ===
//
//				func(s *VersionManagerTestSuite) TestUpdateCRDTCreationError()
//				{
//					update := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "key",
//						Type:         types.UpdateTypeSet,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100},
//						Payload:      &mockCRDT{typ: "unknown"},
//					}
//
//					s.history.EXPECT().HasRange(gomock.Any(), gomock.Any()).Return(false)
//					s.history.EXPECT().AddRange(gomock.Any(), gomock.Any())
//					s.engine.EXPECT().Get(s.ctx, "key").Return(nil, false)
//
//					// Ошибка при создании CRDT
//					s.fabric.EXPECT().
//						New("unknown", "test-node").
//						Return(nil, errors.New("unknown CRDT type"))
//
//					applied := s.vm.Update(s.ctx, update)
//					s.Len(applied, 0)
//				}
//
//				// === VectorClock операции ===
//
//				func(s *VersionManagerTestSuite) TestVectorClockContiguous()
//				{
//					s.vm.seq.Store(42)
//
//					remoteVC := types.VectorClock{"remote-node": 100}
//					s.history.EXPECT().
//						VectorClockContiguous().
//						Return(remoteVC)
//
//					vc := s.vm.VectorClockContiguous()
//
//					s.Equal(uint64(42), vc["test-node"])
//					s.Equal(uint64(100), vc["remote-node"])
//				}
//
//				func(s *VersionManagerTestSuite) TestVectorClockMax()
//				{
//					s.vm.seq.Store(42)
//
//					remoteVC := types.VectorClock{"remote-node": 100}
//					s.history.EXPECT().
//						VectorClockMax().
//						Return(remoteVC)
//
//					vc := s.vm.VectorClockMax()
//
//					s.Equal(uint64(42), vc["test-node"])
//					s.Equal(uint64(100), vc["remote-node"])
//				}
//
//				func(s *VersionManagerTestSuite) TestVersionDiff()
//				{
//					remoteVC := types.VectorClock{
//						"remote-node": 50,
//						"other-node":  30,
//					}
//
//					expected := map[string][]structs.Range{
//						"remote-node": {{Start: 51, End: 100}},
//					}
//
//					s.history.EXPECT().
//						DiffAll(remoteVC).
//						Return(expected)
//
//					diff := s.vm.VersionDiff(remoteVC)
//					s.Equal(expected, diff)
//				}
//
//				// === RestoreSeq ===
//
//				func(s *VersionManagerTestSuite) TestRestoreSeq()
//				{
//					s.history.EXPECT().
//						Clear("remote-node").
//						Times(1)
//
//					s.vm.RestoreSeq("remote-node")
//				}
//
//				// === Integration Tests (без моков) ===
//
//				func
//				TestVersionManagerIntegration(t * testing.T)
//				{
//					// Используем реальные имплементации
//					eng := NewEngine(
//						WithInitialShards(4),
//						WithNodeID("test-node"),
//					)
//					defer eng.Stop()
//
//					fabric := crdt.NewFabric()
//					vm := NewVersionManager("test-node", eng, fabric)
//
//					ctx := context.Background()
//
//					// Test 1: Set new key
//					update1 := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 1, End: 1},
//						Key:          "counter:1",
//						Type:         types.UpdateTypeSet,
//						TimeStamp:    &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      crdt.NewGCounter("test-node"),
//					}
//
//					applied := vm.Update(ctx, update1)
//					assert.Len(t, applied, 1)
//
//					// Verify entry exists
//					entry, ok := eng.Get(ctx, "counter:1")
//					require.True(t, ok)
//					assert.NotNil(t, entry)
//					assert.False(t, entry.Tombstone)
//
//					// Test 2: Delta update
//					counter, _ := entry.Object.(*crdt.GCounter)
//					counter.Increment("remote-node", 5)
//
//					update2 := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 2, End: 2},
//						Key:          "counter:1",
//						Type:         types.UpdateTypeDelta,
//						TimeStamp:    &hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
//						Payload:      counter,
//					}
//
//					applied = vm.Update(ctx, update2)
//					assert.Len(t, applied, 1)
//
//					// Test 3: Delete
//					update3 := &types.Update{
//						NodeID:       "remote-node",
//						Range:        structs.Range{Start: 3, End: 3},
//						Key:          "counter:1",
//						Type:         types.UpdateTypeDelete,
//						TimeStamp:    &hlc.Timestamp{WallTime: 120, Lamport: 0, ID: "remote-node"},
//						SetTimeStamp: &hlc.Timestamp{WallTime: 120, Lamport: 0, ID: "remote-node"},
//					}
//
//					applied = vm.Update(ctx, update3)
//					assert.Len(t, applied, 1)
//
//					// Verify deleted
//					entry, ok = eng.Get(ctx, "counter:1")
//					assert.False(t, ok)
//					assert.Nil(t, entry)
//				}
//
//				func
//				TestVersionManagerConcurrency(t * testing.T)
//				{
//					eng := NewEngine(
//						WithInitialShards(8),
//						WithNodeID("test-node"),
//					)
//					defer eng.Stop()
//
//					fabric := crdt.NewFabric()
//					vm := NewVersionManager("test-node", eng, fabric)
//
//					ctx := context.Background()
//					const numGoroutines = 10
//					const updatesPerGoroutine = 100
//
//					// Параллельно отправляем updates
//					done := make(chan bool, numGoroutines)
//
//					for i := 0; i < numGoroutines; i++ {
//						go func(id int) {
//							defer func() { done <- true }()
//
//							for j := 0; j < updatesPerGoroutine; j++ {
//								update := &types.Update{
//									NodeID: "remote-node",
//									Range:  structs.Range{Start: uint64(id*updatesPerGoroutine + j + 1), End: uint64(id*updatesPerGoroutine + j + 1)},
//									Key:    fmt.Sprintf("key-%d-%d", id, j),
//									Type:   types.UpdateTypeSet,
//									TimeStamp: &hlc.Timestamp{
//										WallTime: uint64(time.Now().UnixNano()),
//										Lamport:  uint64(j),
//										ID:       "remote-node",
//									},
//									SetTimeStamp: &hlc.Timestamp{
//										WallTime: uint64(time.Now().UnixNano()),
//										Lamport:  uint64(j),
//										ID:       "remote-node",
//									},
//									Payload: crdt.NewGCounter("test-node"),
//								}
//
//								vm.Update(ctx, update)
//							}
//						}(i)
//					}
//
//					// Ждём завершения
//					for i := 0; i < numGoroutines; i++ {
//						<-done
//					}
//
//					// Проверяем что все ключи доступны
//					count := 0
//					for i := 0; i < numGoroutines; i++ {
//						for j := 0; j < updatesPerGoroutine; j++ {
//							key := fmt.Sprintf("key-%d-%d", i, j)
//							if _, ok := eng.Get(ctx, key); ok {
//								count++
//							}
//						}
//					}
//
//					assert.Equal(t, numGoroutines*updatesPerGoroutine, count)
//				}
