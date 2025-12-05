package v1

import (
	"context"
	"fmt"
	"in-memorydb/pkg/crdt/hlc"
	"in-memorydb/pkg/storage/engine"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Integration Tests ===

func TestFullLifecycle(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("lifecycle-test"),
		WithDeleteThreshold(50*time.Millisecond),
	)
	ctx := context.Background()
	_ = e.Start(ctx)
	defer e.Stop()

	// 1. Create
	obj := &MockCRDT{value: "initial"}
	ts1 := e.Put(ctx, "test-key", obj, nil)
	require.NotNil(t, ts1)

	// 2. Read
	entry, ok := e.Get(ctx, "test-key")
	require.True(t, ok)
	assert.Equal(t, "initial", entry.Object.(*MockCRDT).value)

	// 3. Update
	obj2 := &MockCRDT{value: "updated"}
	ts2 := e.Put(ctx, "test-key", obj2, nil)
	require.NotNil(t, ts2)
	assert.True(t, ts2.After(ts1))

	entry2, ok := e.Get(ctx, "test-key")
	require.True(t, ok)
	assert.Equal(t, "updated", entry2.Object.(*MockCRDT).value)

	// 4. Delete
	deleted := e.Delete(ctx, "test-key")
	assert.True(t, deleted)

	// 5. Verify tombstone
	entry3, ok := e.Get(ctx, "test-key")
	assert.False(t, ok)
	assert.Nil(t, entry3)

	// 6. Wait for GC
	time.Sleep(200 * time.Millisecond)

	// 7. Verify physical deletion
	shard := e.shardFor("test-key")
	shard.mu.RLock()
	_, exists := shard.data["test-key"]
	shard.mu.RUnlock()
	assert.False(t, exists)
}

func TestMultiNodeSimulation(t *testing.T) {
	// Симулируем 3 узла
	nodes := make([]*Engine, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NewEngine(
			WithInitialShards(4),
			WithNodeID(fmt.Sprintf("node-%d", i)),
		)
		defer nodes[i].Stop()
	}

	ctx := context.Background()

	// Каждый узел записывает свои данные
	for i, node := range nodes {
		for j := 0; j < 10; j++ {
			key := fmt.Sprintf("key-%d-%d", i, j)
			obj := &MockCRDT{value: fmt.Sprintf("value-%d-%d", i, j)}
			node.Put(ctx, key, obj, nil)
		}
	}

	// Симулируем репликацию: копируем данные между узлами
	for i, sourceNode := range nodes {
		for targetIdx, targetNode := range nodes {
			if i == targetIdx {
				continue
			}

			// Получаем все шарды source узла
			sourceShards := *sourceNode.shards.Load()
			for _, shard := range sourceShards {
				if shard == nil {
					continue
				}

				shard.mu.RLock()
				for key, entry := range shard.data {
					if !entry.Tombstone {
						// Синхронизируем timestamp
						syncedTS := targetNode.Clock().SyncWithRemote(entry.SetTimeStamp)

						// Копируем объект
						objCopy := &MockCRDT{value: entry.Object.(*MockCRDT).value}
						targetNode.PutWithTimeStamp(ctx, syncedTS, key, objCopy, nil)
					}
				}
				shard.mu.RUnlock()
			}
		}
	}

	// Проверяем что все узлы имеют все данные
	for i := 0; i < 3; i++ {
		for j := 0; j < 10; j++ {
			key := fmt.Sprintf("key-%d-%d", i, j)

			for nodeIdx, node := range nodes {
				entry, ok := node.Get(ctx, key)
				assert.True(t, ok, "node %d should have key %s", nodeIdx, key)
				if ok {
					assert.NotNil(t, entry)
				}
			}
		}
	}
}

func TestGarbageCollectionUnderLoad(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("gc-test"),
		WithDeleteThreshold(50*time.Millisecond),
	)
	ctx := context.Background()

	_ = e.Start(ctx)
	defer e.Stop()

	const numKeys = 1000
	var deletedKeys atomic.Int32

	// Создаём и удаляем ключи параллельно
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer
	go func() {
		defer wg.Done()
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("key-%d", i)
			obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
			e.Put(ctx, key, obj, nil)
			time.Sleep(time.Microsecond * 100)
		}
	}()

	// Deleter
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond) // Даём время на создание ключей

		for i := 0; i < numKeys/2; i++ {
			key := fmt.Sprintf("key-%d", i)
			if e.Delete(ctx, key) {
				deletedKeys.Add(1)
			}
			time.Sleep(time.Microsecond * 200)
		}
	}()

	wg.Wait()

	// Ждём GC
	time.Sleep(200 * time.Millisecond)

	// Проверяем что удалённые ключи действительно удалены
	physicallyDeletedCount := 0
	for i := 0; i < int(deletedKeys.Load()); i++ {
		key := fmt.Sprintf("key-%d", i)
		shard := e.shardFor(key)
		shard.mu.RLock()
		_, exists := shard.data[key]
		shard.mu.RUnlock()

		if !exists {
			physicallyDeletedCount++
		}
	}

	// Должна быть удалена хотя бы часть ключей
	assert.Greater(t, physicallyDeletedCount, 0)
}

func TestTimestampConsistency(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("ts-test"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Записываем много значений быстро
	const numOps = 1000
	timestamps := make([]*hlc.Timestamp, numOps)

	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		timestamps[i] = e.Put(ctx, key, obj, nil)
	}

	// Проверяем что timestamps строго монотонны
	for i := 1; i < len(timestamps); i++ {
		cmp := hlc.Compare(timestamps[i-1], timestamps[i])
		assert.NotEqual(t, hlc.Greater, cmp,
			"timestamp %d should not be greater than timestamp %d", i-1, i)
	}
}

func TestCallbackOrdering(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("callback-test"),
	)
	defer e.Stop()

	ctx := context.Background()

	var mu sync.Mutex
	callbackOrder := make([]int, 0)

	// Создаём callback который записывает порядок вызовов
	makeCallback := func(id int) engine.Callback {
		return func(entry *engine.CRDTEntry) {
			mu.Lock()
			callbackOrder = append(callbackOrder, id)
			mu.Unlock()
		}
	}

	// Записываем несколько значений с callbacks
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		e.Put(ctx, key, obj, makeCallback(i))
	}

	// Проверяем что все callbacks вызваны
	assert.Equal(t, 10, len(callbackOrder))

	// Проверяем что порядок соответствует порядку вызовов Put
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, callbackOrder)
}

func TestStopWithPendingOperations(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("stop-test"),
		WithDeleteThreshold(100*time.Millisecond),
	)

	ctx := context.Background()

	// Создаём и удаляем много ключей
	const numOps = 1000
	for i := 0; i < numOps; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		e.Put(ctx, key, obj, nil)
		if i%2 == 0 {
			e.Delete(ctx, key)
		}
	}

	// Немедленная остановка
	e.Stop()

	// Не должно быть panic
	// После Stop новые операции могут не работать, но это нормально
}

func TestEmptyEngineOperations(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("empty-test"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Get на пустом engine
	entry, ok := e.Get(ctx, "nonexistent")
	assert.False(t, ok)
	assert.Nil(t, entry)

	// Delete на пустом engine
	deleted := e.Delete(ctx, "nonexistent")
	assert.False(t, deleted)

	// Проверяем что engine остался в консистентном состоянии
	obj := &MockCRDT{value: "test"}
	ts := e.Put(ctx, "test-key", obj, nil)
	assert.NotNil(t, ts)

	entry, ok = e.Get(ctx, "test-key")
	assert.True(t, ok)
	assert.NotNil(t, entry)
}

func TestLargeKeyValues(t *testing.T) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("large-test"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Очень длинный ключ
	longKey := make([]byte, 10000)
	for i := range longKey {
		longKey[i] = byte('a' + (i % 26))
	}

	// Большое значение
	longValue := make([]byte, 100000)
	for i := range longValue {
		longValue[i] = byte('0' + (i % 10))
	}

	obj := &MockCRDT{value: string(longValue)}
	ts := e.Put(ctx, string(longKey), obj, nil)
	require.NotNil(t, ts)

	entry, ok := e.Get(ctx, string(longKey))
	require.True(t, ok)
	assert.Equal(t, string(longValue), entry.Object.(*MockCRDT).value)

	// Delete
	deleted := e.Delete(ctx, string(longKey))
	assert.True(t, deleted)
}

func TestTimestampSyncAcrossNodes(t *testing.T) {
	// Создаём два engine с разными NodeID
	e1 := NewEngine(
		WithInitialShards(4),
		WithNodeID("node-1"),
	)
	defer e1.Stop()

	e2 := NewEngine(
		WithInitialShards(4),
		WithNodeID("node-2"),
	)
	defer e2.Stop()

	ctx := context.Background()

	// Node 1 создаёт запись
	obj1 := &MockCRDT{value: "from-node-1"}
	ts1 := e1.Put(ctx, "shared-key", obj1, nil)

	// Node 2 синхронизируется с timestamp от Node 1
	syncedTS := e2.Clock().SyncWithRemote(ts1)

	// Timestamp Node 2 должен быть >= timestamp Node 1
	assert.False(t, syncedTS.Before(ts1))

	// Node 2 создаёт свою запись с синхронизированным timestamp
	obj2 := &MockCRDT{value: "from-node-2"}
	e2.PutWithTimeStamp(ctx, syncedTS, "shared-key", obj2, nil)

	// Проверяем что timestamps правильно упорядочены
	entry1, _ := e1.Get(ctx, "shared-key")
	entry2, _ := e2.Get(ctx, "shared-key")

	assert.False(t, entry2.SetTimeStamp.Before(entry1.SetTimeStamp))
}
