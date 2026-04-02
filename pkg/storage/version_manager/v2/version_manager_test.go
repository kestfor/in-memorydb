package v2

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionManager_Advance(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)

	// Advance должен возвращать последовательные номера
	assert.Equal(t, uint64(1), vm.Advance("key"))
	assert.Equal(t, uint64(2), vm.Advance("key"))
	assert.Equal(t, uint64(3), vm.Advance("key"))

	// GetCurrentSequence должен возвращать текущее значение
	assert.Equal(t, uint64(3), vm.GetCurrentSequence())
}

func TestVersionManager_AdvanceConcurrent(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)

	const numGoroutines = 100
	const opsPerGoroutine = 1000

	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				vm.Advance("key")
			}
		}()
	}

	wg.Wait()

	// Должны получить точное количество
	expected := uint64(numGoroutines * opsPerGoroutine)
	assert.Equal(t, expected, vm.GetCurrentSequence())
}

func TestVersionManager_Update(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	counter := crdt.NewPNCounter("test-node")
	counterDelta := counter.Increment(1)

	update1 := types.Update{
		NodeID:       "remote-node",
		Seq:          1,
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      counterDelta,
	}

	applied := vm.Update(ctx, update1)
	assert.Len(t, applied, 1)

	// Verify entry exists
	entry, ok := eng.Get(ctx, "counter:1")
	require.True(t, ok)
	assert.NotNil(t, entry)
	assert.False(t, entry.Tombstone)
}

func TestVersionManager_UpdateDuplicate(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	counter := crdt.NewPNCounter("test-node")
	counterDelta := counter.Increment(1)

	update := types.Update{
		NodeID:       "remote-node",
		Seq:          1,
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      counterDelta,
	}

	// Первое применение
	applied := vm.Update(ctx, update)
	assert.Len(t, applied, 1)

	// Повторное применение того же update - должен быть отклонён
	applied = vm.Update(ctx, update)
	assert.Len(t, applied, 0)
}

func TestVersionManager_UpdateDelta(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Создаём entry через Set update
	counter := crdt.NewPNCounter("remote-node")
	counterDelta := counter.Increment(5)

	update1 := types.Update{
		NodeID:       "remote-node",
		Seq:          1,
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      counterDelta,
	}

	applied := vm.Update(ctx, update1)
	assert.Len(t, applied, 1)

	// Применяем Delta update от того же counter (продолжаем инкремент)
	// Это симулирует корректную последовательность операций от одной ноды
	delta2 := counter.Increment(10)

	update2 := types.Update{
		NodeID:       "remote-node",
		Seq:          2,
		Key:          "counter:1",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      delta2,
	}

	applied = vm.Update(ctx, update2)
	assert.Len(t, applied, 1)

	// Проверяем что delta применилась
	entry, ok := eng.Get(ctx, "counter:1")
	require.True(t, ok)
	assert.NotNil(t, entry)

	pnc, ok := entry.Object.(*crdt.PNCounter)
	require.True(t, ok)
	// 5 + 10 = 15 (используем тот же counter, так что инкременты накапливаются)
	assert.Equal(t, int64(15), pnc.Value())
}

func TestVersionManager_UpdateDelete(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
		enginev1.WithDeleteThreshold(time.Millisecond*100),
	)
	eng.Start(context.Background())
	defer eng.Stop()

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Создаём entry
	counter := crdt.NewPNCounter("remote-node")
	counterDelta := counter.Increment(1)

	update1 := types.Update{
		NodeID:       "remote-node",
		Seq:          1,
		Key:          "counter:1",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
		Payload:      counterDelta,
	}

	vm.Update(ctx, update1)

	// Удаляем
	update2 := types.Update{
		NodeID:       "remote-node",
		Seq:          2,
		Key:          "counter:1",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 110, Lamport: 0, ID: "remote-node"},
		Payload:      &crdt.PNCounterDelta{},
	}

	applied := vm.Update(ctx, update2)
	assert.Len(t, applied, 1)

	// Проверяем что entry помечена как удалённая
	entry, ok := eng.Get(ctx, "counter:1")
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestVersionManager_UpdateParallel(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Создаём много updates для разных ключей
	const numUpdates = 100
	updates := make([]types.Update, numUpdates)

	for i := 0; i < numUpdates; i++ {
		counter := crdt.NewPNCounter("remote-node")
		counterDelta := counter.Increment(int64(i))

		updates[i] = types.Update{
			NodeID:       "remote-node",
			Seq:          uint64(i) + 1,
			Key:          "counter:" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			Type:         types.UpdateTypeSet,
			TimeStamp:    hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote-node"},
			SetTimeStamp: hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote-node"},
			Payload:      counterDelta,
		}
	}

	// Применяем все updates (должно использовать параллельную обработку)
	applied := vm.Update(ctx, updates...)
	assert.Len(t, applied, numUpdates)

	// Проверяем что все entries созданы
	for _, u := range updates {
		entry, ok := eng.Get(ctx, u.Key)
		require.True(t, ok, "entry not found for key: %s", u.Key)
		assert.NotNil(t, entry)
	}
}

func TestVersionManager_VectorClockContiguous(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Локальные операции
	vm.Advance("key")
	vm.Advance("key")
	vm.Advance("key")

	// Remote updates
	counter := crdt.NewPNCounter("remote-node")
	counterDelta := counter.Increment(1)

	for i := 1; i <= 5; i++ {
		update := types.Update{
			NodeID:       "remote-node",
			Seq:          uint64(i),
			Key:          "key",
			Type:         types.UpdateTypeDelta,
			TimeStamp:    hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote-node"},
			SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
			Payload:      counterDelta,
		}
		vm.Update(ctx, update)
	}

	vc := vm.KeyVersionClock("key")
	assert.Equal(t, map[string]uint64{
		"test-node":   3,
		"remote-node": 5,
	}, vc)

	//assert.Equal(t, uint64(3), vc["test-node"])
	//assert.Equal(t, uint64(5), vc["remote-node"])
}

func TestVersionManager_VectorClockMax(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	vm.Advance("key")
	vm.Advance("key")

	// Remote updates с дыркой
	counter := crdt.NewPNCounter("remote-node")

	for _, seq := range []uint64{1, 2, 5, 6, 10} {
		update := types.Update{
			NodeID:       "remote-node",
			Seq:          seq,
			Key:          "key",
			Type:         types.UpdateTypeDelta,
			TimeStamp:    hlc.Timestamp{WallTime: 100 + seq, Lamport: 0, ID: "remote-node"},
			SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
			Payload:      counter.Increment(1),
		}
		vm.Update(ctx, update)
	}

	vcMax := vm.VectorClockMax()
	vcContiguous := vm.VectorClockContiguous()

	assert.Equal(t, uint64(2), vcMax["test-node"])
	assert.Equal(t, uint64(10), vcMax["remote-node"])
	assert.Equal(t, uint64(2), vcContiguous["test-node"])
	assert.Equal(t, uint64(2), vcContiguous["remote-node"]) // дырка после 2
}

func TestVersionManager_VersionDiff(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Remote updates [1-5]
	counter := crdt.NewPNCounter("remote-node")

	for i := 1; i <= 5; i++ {
		update := types.Update{
			NodeID:       "remote-node",
			Seq:          uint64(i),
			Key:          "key",
			Type:         types.UpdateTypeDelta,
			TimeStamp:    hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote-node"},
			SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
			Payload:      counter.Increment(1),
		}
		vm.Update(ctx, update)
	}

	// Remote утверждает что имеет до 10
	remote := types.VectorClock{
		"remote-node": 10,
		"other-node":  5,
	}

	diff := vm.VersionDiff(remote)

	// Нам нужны [6-10] для remote-node и [1-5] для other-node
	require.Len(t, diff["remote-node"], 1)
	assert.Equal(t, structs.Range{Start: 6, End: 10}, diff["remote-node"][0])

	require.Len(t, diff["other-node"], 1)
	assert.Equal(t, structs.Range{Start: 1, End: 5}, diff["other-node"][0])
}

func TestVersionManager_ComplexConflictResolution(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
		enginev1.WithDeleteThreshold(time.Millisecond*500),
	)
	eng.Start(context.Background())
	defer eng.Stop()

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Сценарий: две ноды конкурируют за один ключ
	// node1: Set counter, Increment
	// node2: Set register, Delete
	// Delete с более поздним timestamp должен выиграть

	upd1 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          1,
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd2 := types.Update{
		NodeID:       "remote-node-2",
		Seq:          1,
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-2"},
		SetTimeStamp: hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-2"},
		Payload:      &crdt.LWWHLCRegisterDelta{},
	}

	counterDelta := crdt.NewPNCounter("remote-node-1").Increment(10)
	upd3 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          2,
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		Payload:      counterDelta,
	}

	upd4 := types.Update{
		NodeID:       "remote-node-2",
		Seq:          2,
		Key:          "key",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    hlc.Timestamp{WallTime: 104, Lamport: 0, ID: "remote-node-2"},
		SetTimeStamp: hlc.Timestamp{WallTime: 104, Lamport: 0, ID: "remote-node-2"},
		Payload:      &crdt.LWWHLCRegisterDelta{},
	}

	// Применяем в порядке
	applied := vm.Update(ctx, upd1, upd2, upd3, upd4)
	assert.Len(t, applied, 4)

	// Ключ должен быть удалён
	entry, ok := eng.Get(ctx, "key")
	assert.False(t, ok)
	assert.Nil(t, entry)
}

func TestVersionManager_ComplexConflictResolution2(t *testing.T) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(4),
		enginev1.WithNodeID("test-node"),
		enginev1.WithDeleteThreshold(time.Minute),
	)
	eng.Start(context.Background())
	defer eng.Stop()

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Сценарий: создается счетчик, удаляется на одной, на другой, создается на первой, должен создаться на второй

	upd1 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          1,
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 101, Lamport: 0, ID: "remote-node-1"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd2 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          2,
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 102, Lamport: 0, ID: "remote-node-1"},
		Payload: &crdt.PNCounterDelta{P: map[string]int64{
			"remote-node-1": 10,
		}},
	}

	upd3 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          3,
		Key:          "key",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 103, Lamport: 0, ID: "remote-node-1"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd4 := types.Update{
		NodeID:       "test-node",
		Seq:          1,
		Key:          "key",
		Type:         types.UpdateTypeDelete,
		TimeStamp:    hlc.Timestamp{WallTime: 105, Lamport: 0, ID: "test-node"},
		SetTimeStamp: hlc.Timestamp{WallTime: 105, Lamport: 0, ID: "test-node"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd5 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          4,
		Key:          "key",
		Type:         types.UpdateTypeDelta,
		TimeStamp:    hlc.Timestamp{WallTime: 107, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 107, Lamport: 0, ID: "remote-node-1"},
		Payload:      &crdt.PNCounterDelta{},
	}

	upd6 := types.Update{
		NodeID:       "remote-node-1",
		Seq:          5,
		Key:          "key",
		Type:         types.UpdateTypeSet,
		TimeStamp:    hlc.Timestamp{WallTime: 108, Lamport: 0, ID: "remote-node-1"},
		SetTimeStamp: hlc.Timestamp{WallTime: 108, Lamport: 0, ID: "remote-node-1"},
		Payload: &crdt.PNCounterDelta{P: map[string]int64{
			"remote-node-1": 10,
		}},
	}

	// Применяем в порядке
	_ = vm.Update(ctx, upd1, upd2, upd3, upd4, upd5, upd6)

	// Ключ должен существовать
	entry, ok := eng.Get(ctx, "key")
	require.True(t, ok)
	require.NotNil(t, entry)
	assert.False(t, entry.Tombstone)
}

// Benchmarks

func BenchmarkVersionManager_Advance(b *testing.B) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.Advance("key")
	}
}

func BenchmarkVersionManager_AdvanceParallel(b *testing.B) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			vm.Advance("key")
		}
	})
}

func BenchmarkVersionManager_Update(b *testing.B) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter := crdt.NewPNCounter("remote-node")
		update := types.Update{
			NodeID:       "remote-node",
			Seq:          uint64(i) + 1,
			Key:          "key",
			Type:         types.UpdateTypeDelta,
			TimeStamp:    hlc.Timestamp{WallTime: uint64(100 + i), Lamport: 0, ID: "remote-node"},
			SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
			Payload:      counter.Increment(1),
		}
		vm.Update(ctx, update)
	}
}

func BenchmarkVersionManager_UpdateBatch(b *testing.B) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	const batchSize = 100
	updates := make([]types.Update, batchSize)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Создаём батч updates
		for j := 0; j < batchSize; j++ {
			seq := uint64(i*batchSize + j + 1)
			counter := crdt.NewPNCounter("remote-node")
			updates[j] = types.Update{
				NodeID:       "remote-node",
				Seq:          uint64(seq),
				Key:          "key:" + string(rune('A'+j%26)),
				Type:         types.UpdateTypeDelta,
				TimeStamp:    hlc.Timestamp{WallTime: seq, Lamport: 0, ID: "remote-node"},
				SetTimeStamp: hlc.Timestamp{WallTime: 100, Lamport: 0, ID: "remote-node"},
				Payload:      counter.Increment(1),
			}
		}
		vm.Update(ctx, updates...)
	}
}

func BenchmarkVersionManager_VectorClockContiguous(b *testing.B) {
	eng := enginev1.NewEngine(
		enginev1.WithInitialShards(256),
		enginev1.WithNodeID("test-node"),
	)

	vm := NewVersionManager("test-node", eng)
	ctx := context.Background()

	// Добавляем данные от нескольких нод
	for node := 0; node < 10; node++ {
		nodeID := "node" + string(rune('A'+node))
		for i := 1; i <= 100; i++ {
			counter := crdt.NewPNCounter(nodeID)
			update := types.Update{
				NodeID:       nodeID,
				Seq:          uint64(i),
				Key:          "key",
				Type:         types.UpdateTypeDelta,
				TimeStamp:    hlc.Timestamp{WallTime: uint64(i), Lamport: 0, ID: nodeID},
				SetTimeStamp: hlc.Timestamp{WallTime: 1, Lamport: 0, ID: nodeID},
				Payload:      counter.Increment(1),
			}
			vm.Update(ctx, update)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.VectorClockContiguous()
	}
}
