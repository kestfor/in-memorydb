package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/crdt/hlc"
	"in-memorydb/pkg/storage/engine"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type MockCRDT struct {
	value string
}

func (m *MockCRDT) Merge(other crdt.CRDT) error {
	return nil
}

func (m *MockCRDT) Value() any {
	return m.value
}

func (m *MockCRDT) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.value)
}

func (m *MockCRDT) UnmarshalJSON(data []byte) error {
	err := json.Unmarshal(data, &m.value)
	return err
}

func (m *MockCRDT) Type() crdt.CRDTType {
	return crdt.CRDTTypePNCounter
}

func (m *MockCRDT) ApplyDelta(d crdt.Delta) error {
	return nil
}

// Test Suite
type EngineTestSuite struct {
	suite.Suite
	engine *Engine
	ctx    context.Context
}

func (s *EngineTestSuite) SetupTest() {
	s.ctx = context.Background()
	s.engine = NewEngine(
		WithInitialShards(4),
		WithNodeID("test-node"),
		WithDeleteThreshold(100*time.Millisecond),
	).(*Engine)
	s.Require().NoError(s.engine.Start(s.ctx))
}

func (s *EngineTestSuite) TearDownTest() {
	if s.engine != nil {
		s.engine.Stop()
	}
}

func TestEngineTestSuite(t *testing.T) {
	suite.Run(t, new(EngineTestSuite))
}

// === Базовые операции ===

func (s *EngineTestSuite) TestPutAndGet() {
	obj := &MockCRDT{value: "test-value"}

	ts := s.engine.Put(s.ctx, "key1", obj, nil)
	s.NotNil(ts)

	entry, ok := s.engine.Get(s.ctx, "key1")
	s.True(ok)
	s.NotNil(entry)
	s.Equal("test-value", entry.Object.(*MockCRDT).value)
	s.False(entry.Tombstone)
}

func (s *EngineTestSuite) TestGetNonExistent() {
	entry, ok := s.engine.Get(s.ctx, "nonexistent")
	s.False(ok)
	s.Nil(entry)
}

func (s *EngineTestSuite) TestPutOverwrite() {
	obj1 := &MockCRDT{value: "value1"}
	obj2 := &MockCRDT{value: "value2"}

	s.engine.Put(s.ctx, "key1", obj1, nil)
	s.engine.Put(s.ctx, "key1", obj2, nil)

	entry, ok := s.engine.Get(s.ctx, "key1")
	s.True(ok)
	s.Equal("value2", entry.Object.(*MockCRDT).value)
}

func (s *EngineTestSuite) TestDelete() {
	obj := &MockCRDT{value: "test"}
	s.engine.Put(s.ctx, "key1", obj, nil)

	_, deleted := s.engine.Delete(s.ctx, "key1")
	s.True(deleted)

	entry, ok := s.engine.Get(s.ctx, "key1")
	s.True(ok)
	s.True(entry.Deleted())
}

func (s *EngineTestSuite) TestDeleteNonExistent() {
	_, deleted := s.engine.Delete(s.ctx, "nonexistent")
	s.False(deleted)
}

func (s *EngineTestSuite) TestDeleteTwice() {
	obj := &MockCRDT{value: "test"}
	s.engine.Put(s.ctx, "key1", obj, nil)

	_, deleted1 := s.engine.Delete(s.ctx, "key1")
	s.True(deleted1)

	_, deleted2 := s.engine.Delete(s.ctx, "key1")
	s.True(deleted2)
}

// === Callback тестирование ===

func (s *EngineTestSuite) TestPutWithCallback() {
	called := false
	var capturedEntry *engine.CRDTEntry

	callback := func(entry *engine.CRDTEntry) {
		called = true
		capturedEntry = entry
	}

	obj := &MockCRDT{value: "test"}
	s.engine.Put(s.ctx, "key1", obj, callback)

	s.True(called)
	s.NotNil(capturedEntry)
	s.Equal("test", capturedEntry.Object.(*MockCRDT).value)
}

// === Garbage Collection ===

func (s *EngineTestSuite) TestGarbageCollection() {
	obj := &MockCRDT{value: "test"}
	s.engine.Put(s.ctx, "key1", obj, nil)

	s.engine.Delete(s.ctx, "key1")

	// Ждём пока GC удалит tombstone
	time.Sleep(200 * time.Millisecond)

	// Проверяем что ключ действительно удалён из хранилища
	shard := s.engine.shardFor("key1")
	shard.mu.RLock()
	_, exists := shard.data["key1"]
	shard.mu.RUnlock()

	s.False(exists, "tombstone should be garbage collected")
}

func (s *EngineTestSuite) TestGarbageCollectionMultipleKeys() {
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		s.engine.Put(s.ctx, key, &MockCRDT{value: fmt.Sprintf("value%d", i)}, nil)
		s.engine.Delete(s.ctx, key)
	}

	time.Sleep(time.Second * 1)

	// Проверяем что все удалены
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		entry, ok := s.engine.Get(s.ctx, key)
		s.False(ok)
		s.Nil(entry)
	}
}

// === Concurrency Tests ===

func (s *EngineTestSuite) TestConcurrentPuts() {
	const numGoroutines = 100
	const keysPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j)
				obj := &MockCRDT{value: fmt.Sprintf("value-%d-%d", id, j)}
				s.engine.Put(s.ctx, key, obj, nil)
			}
		}(i)
	}

	wg.Wait()

	// Проверяем что все ключи доступны
	count := 0
	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := fmt.Sprintf("key-%d-%d", i, j)
			if _, ok := s.engine.Get(s.ctx, key); ok {
				count++
			}
		}
	}

	s.Equal(numGoroutines*keysPerGoroutine, count)
}

func (s *EngineTestSuite) TestConcurrentPutsSameKey() {
	const numGoroutines = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			obj := &MockCRDT{value: fmt.Sprintf("value-%d", id)}
			s.engine.Put(s.ctx, "shared-key", obj, nil)
		}(i)
	}

	wg.Wait()

	entry, ok := s.engine.Get(s.ctx, "shared-key")
	s.True(ok)
	s.NotNil(entry)
}

func (s *EngineTestSuite) TestConcurrentMixedOperations() {
	const numGoroutines = 50
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d", j%10)

				switch j % 3 {
				case 0:
					obj := &MockCRDT{value: fmt.Sprintf("value-%d-%d", id, j)}
					s.engine.Put(s.ctx, key, obj, nil)
				case 1:
					s.engine.Get(s.ctx, key)
				case 2:
					s.engine.Delete(s.ctx, key)
				}
			}
		}(i)
	}

	wg.Wait()
}

// === Sharding Tests ===

func (s *EngineTestSuite) TestShardDistribution() {
	numKeys := 1000
	shardCounts := make(map[int]int)

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		s.engine.Put(s.ctx, key, obj, nil)

		shard := s.engine.shardFor(key)
		shardIdx := -1
		arr := *s.engine.shards.Load()
		for idx, sh := range arr {
			if sh == shard {
				shardIdx = idx
				break
			}
		}
		shardCounts[shardIdx]++
	}

	// Проверяем что ключи распределены по шардам
	s.True(len(shardCounts) > 1, "keys should be distributed across multiple shards")
}

// === HLC Timestamp Tests ===

func (s *EngineTestSuite) TestTimestampMonotonicity() {
	timestamps := make([]*hlc.Timestamp, 100)

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		timestamps[i] = s.engine.Put(s.ctx, key, obj, nil)
	}

	// Проверяем что timestamps монотонно возрастают
	for i := 1; i < len(timestamps); i++ {
		s.True(
			timestamps[i].After(timestamps[i-1]) || timestamps[i].Equal(timestamps[i-1]),
			"timestamps should be monotonic",
		)
	}
}

func (s *EngineTestSuite) TestPutWithTimestamp() {
	clock := s.engine.Clock()
	ts := clock.Now()

	// Искусственно делаем timestamp "из будущего"
	futureTS := &hlc.Timestamp{
		WallTime: ts.WallTime + uint64(time.Second),
		Lamport:  ts.Lamport + 1,
		ID:       ts.ID,
	}

	obj := &MockCRDT{value: "test"}
	returnedTS := s.engine.PutWithTimeStamp(s.ctx, futureTS, "key1", obj, nil)

	s.Equal(futureTS.WallTime, returnedTS.WallTime)
	s.Equal(futureTS.Lamport, returnedTS.Lamport)

	entry, ok := s.engine.Get(s.ctx, "key1")
	s.True(ok)
	s.Equal(futureTS.WallTime, entry.SetTimeStamp.WallTime)
}

// === Edge Cases ===

func (s *EngineTestSuite) TestEmptyKey() {
	obj := &MockCRDT{value: "test"}
	ts := s.engine.Put(s.ctx, "", obj, nil)
	s.NotNil(ts)

	entry, ok := s.engine.Get(s.ctx, "")
	s.True(ok)
	s.NotNil(entry)
}

func (s *EngineTestSuite) TestVeryLongKey() {
	longKey := string(make([]byte, 1000))
	for i := range longKey {
		longKey = longKey[:i] + "a" + longKey[i+1:]
	}

	obj := &MockCRDT{value: "test"}
	s.engine.Put(s.ctx, longKey, obj, nil)

	entry, ok := s.engine.Get(s.ctx, longKey)
	s.True(ok)
	s.NotNil(entry)
}

func (s *EngineTestSuite) TestNilCallback() {
	obj := &MockCRDT{value: "test"}
	ts := s.engine.Put(s.ctx, "key1", obj, nil)
	s.NotNil(ts)
}

//func (s *EngineTestSuite) TestCountKeysAccuracy() {
//	initialCount := s.engine.countKeys.Load()
//
//	for i := 0; i < 10; i++ {
//		key := fmt.Sprintf("key-%d", i)
//		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
//		s.engine.Put(s.ctx, key, obj, nil)
//	}
//
//	afterPutCount := s.engine.countKeys.Load()
//	s.Equal(initialCount+10, afterPutCount)
//
//	// Delete and wait for GC
//	for i := 0; i < 5; i++ {
//		key := fmt.Sprintf("key-%d", i)
//		s.engine.Delete(s.ctx, key)
//	}
//
//	time.Sleep(200 * time.Millisecond)
//
//	afterGCCount := s.engine.countKeys.Load()
//	s.Equal(initialCount+5, afterGCCount)
//}

// === Stress Tests ===

func (s *EngineTestSuite) TestStressConcurrentOperations() {
	const numGoroutines = 200
	const opsPerGoroutine = 500

	var wg sync.WaitGroup
	var successfulOps atomic.Int64

	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < opsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d", j%100)

				switch j % 4 {
				case 0:
					obj := &MockCRDT{value: fmt.Sprintf("v-%d-%d", id, j)}
					s.engine.Put(s.ctx, key, obj, nil)
					successfulOps.Add(1)
				case 1:
					if _, ok := s.engine.Get(s.ctx, key); ok {
						successfulOps.Add(1)
					}
				case 2:
					if _, ok := s.engine.Delete(s.ctx, key); ok {
						successfulOps.Add(1)
					}
				case 3:
					obj := &MockCRDT{value: fmt.Sprintf("v2-%d-%d", id, j)}
					s.engine.PutWithTimeStamp(s.ctx, s.engine.Clock().Now(), key, obj, nil)
					successfulOps.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	s.Greater(successfulOps.Load(), int64(0), "should have successful operations")
}

// === Unit Tests (не требуют suite) ===

func TestNewEngine(t *testing.T) {
	e := NewEngine(
		WithInitialShards(8),
		WithNodeID("test"),
		WithDeleteThreshold(time.Minute),
	).(*Engine)
	assert.NotNil(t, e)
	assert.Equal(t, uint32(8), e.numShards.Load())
	assert.NotNil(t, e.clock)
	assert.NotNil(t, e.markChan)
	assert.NotNil(t, e.fallback)
}

func TestNewEngineDefaults(t *testing.T) {
	e := NewEngine().(*Engine)
	assert.Equal(t, uint32(defaultInitialShards), e.numShards.Load())
	assert.Equal(t, defaultDeleteThreshold, e.opts.DeleteThreshold)
}

func TestEngineStop(t *testing.T) {
	e := NewEngine(WithNodeID("test"))

	// Добавляем данные
	ctx := context.Background()
	obj := &MockCRDT{value: "test"}
	e.Put(ctx, "key1", obj, nil)

	// Проверяем что можем прочитать данные (но новые операции могут не работать)
	entry, ok := e.Get(ctx, "key1")
	assert.True(t, ok)
	assert.NotNil(t, entry)
}

func TestShardForConsistency(t *testing.T) {
	e := NewEngine(WithInitialShards(4), WithNodeID("test")).(*Engine)

	// Один и тот же ключ всегда должен попадать в один шард
	shard1 := e.shardFor("test-key")
	shard2 := e.shardFor("test-key")
	shard3 := e.shardFor("test-key")

	assert.Equal(t, shard1, shard2)
	assert.Equal(t, shard2, shard3)
}
