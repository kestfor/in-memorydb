package history

import (
	"sync"
	"testing"

	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShardedHistory_Add(t *testing.T) {
	h := NewShardedHistory()

	h.Add("node1", 1)
	h.Add("node1", 2)
	h.Add("node1", 3)

	assert.True(t, h.Has("node1", 1))
	assert.True(t, h.Has("node1", 2))
	assert.True(t, h.Has("node1", 3))
	assert.False(t, h.Has("node1", 4))
	assert.False(t, h.Has("node2", 1))
}

func TestShardedHistory_AddRange(t *testing.T) {
	h := NewShardedHistory()

	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	h.AddRange("node1", structs.Range{Start: 10, End: 15})

	assert.True(t, h.Has("node1", 1))
	assert.True(t, h.Has("node1", 3))
	assert.True(t, h.Has("node1", 5))
	assert.False(t, h.Has("node1", 6))
	assert.True(t, h.Has("node1", 10))
	assert.True(t, h.Has("node1", 15))
	assert.False(t, h.Has("node1", 16))
}

func TestShardedHistory_TryAddRange(t *testing.T) {
	h := NewShardedHistory()

	// Первое добавление - успех
	assert.True(t, h.TryAddRange("node1", structs.Range{Start: 1, End: 5}))

	// Повторное добавление того же range - неудача
	assert.False(t, h.TryAddRange("node1", structs.Range{Start: 1, End: 5}))
	assert.False(t, h.TryAddRange("node1", structs.Range{Start: 2, End: 4})) // subset

	// Добавление нового range - успех
	assert.True(t, h.TryAddRange("node1", structs.Range{Start: 10, End: 15}))

	// Частичное перекрытие - успех (добавляется недостающая часть)
	assert.True(t, h.TryAddRange("node1", structs.Range{Start: 4, End: 8}))
}

func TestShardedHistory_HasRange(t *testing.T) {
	h := NewShardedHistory()

	h.AddRange("node1", structs.Range{Start: 1, End: 10})

	assert.True(t, h.HasRange("node1", structs.Range{Start: 1, End: 10}))
	assert.True(t, h.HasRange("node1", structs.Range{Start: 2, End: 5}))
	assert.True(t, h.HasRange("node1", structs.Range{Start: 1, End: 1}))
	assert.False(t, h.HasRange("node1", structs.Range{Start: 1, End: 11}))
	assert.False(t, h.HasRange("node1", structs.Range{Start: 11, End: 15}))
	assert.False(t, h.HasRange("node2", structs.Range{Start: 1, End: 5}))
}

func TestShardedHistory_ContiguousSeq(t *testing.T) {
	h := NewShardedHistory()

	// Пустая история
	assert.Equal(t, uint64(0), h.ContiguousSeq("node1"))

	// Contiguous от 1
	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	assert.Equal(t, uint64(5), h.ContiguousSeq("node1"))

	// С дыркой
	h.AddRange("node1", structs.Range{Start: 7, End: 10})
	assert.Equal(t, uint64(5), h.ContiguousSeq("node1"))

	// Заполняем дырку
	h.Add("node1", 6)
	assert.Equal(t, uint64(10), h.ContiguousSeq("node1"))
}

func TestShardedHistory_MaxSeq(t *testing.T) {
	h := NewShardedHistory()

	assert.Equal(t, uint64(0), h.MaxSeq("node1"))

	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	assert.Equal(t, uint64(5), h.MaxSeq("node1"))

	h.AddRange("node1", structs.Range{Start: 10, End: 15})
	assert.Equal(t, uint64(15), h.MaxSeq("node1"))
}

func TestShardedHistory_AllContiguousSeq(t *testing.T) {
	h := NewShardedHistory()

	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	h.AddRange("node2", structs.Range{Start: 1, End: 10})
	h.AddRange("node3", structs.Range{Start: 5, End: 10}) // не начинается с 1

	vc := h.AllContiguousSeq()

	assert.Equal(t, uint64(5), vc["node1"])
	assert.Equal(t, uint64(10), vc["node2"])
	assert.Equal(t, uint64(0), vc["node3"]) // нет покрытия от 1
}

func TestShardedHistory_AllMaxSeq(t *testing.T) {
	h := NewShardedHistory()

	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	h.AddRange("node2", structs.Range{Start: 1, End: 10})
	h.AddRange("node3", structs.Range{Start: 5, End: 15})

	vc := h.AllMaxSeq()

	assert.Equal(t, uint64(5), vc["node1"])
	assert.Equal(t, uint64(10), vc["node2"])
	assert.Equal(t, uint64(15), vc["node3"])
}

func TestShardedHistory_DiffAll(t *testing.T) {
	h := NewShardedHistory()

	// У нас есть [1-5] для node1
	h.AddRange("node1", structs.Range{Start: 1, End: 5})

	// Remote имеет больше
	remote := map[string]uint64{
		"node1": 10, // remote has up to 10
		"node2": 5,  // node2 which we don't have
	}

	diff := h.DiffAll(remote)

	// Для node1 нам нужны [6-10]
	require.Len(t, diff["node1"], 1)
	assert.Equal(t, structs.Range{Start: 6, End: 10}, diff["node1"][0])

	// Для node2 нам нужны [1-5]
	require.Len(t, diff["node2"], 1)
	assert.Equal(t, structs.Range{Start: 1, End: 5}, diff["node2"][0])
}

func TestShardedHistory_DiffAllWithGaps(t *testing.T) {
	h := NewShardedHistory()

	// У нас есть [1-3] и [7-10] для node1 (дырка 4-6)
	h.AddRange("node1", structs.Range{Start: 1, End: 3})
	h.AddRange("node1", structs.Range{Start: 7, End: 10})

	remote := map[string]uint64{
		"node1": 10,
	}

	diff := h.DiffAll(remote)

	// Нам нужны [4-6]
	require.Len(t, diff["node1"], 1)
	assert.Equal(t, structs.Range{Start: 4, End: 6}, diff["node1"][0])
}

func TestShardedHistory_Clear(t *testing.T) {
	h := NewShardedHistory()

	h.AddRange("node1", structs.Range{Start: 1, End: 10})
	assert.True(t, h.Has("node1", 5))

	h.Clear("node1")
	assert.False(t, h.Has("node1", 5))
}

func TestShardedHistory_ConcurrentAccess(t *testing.T) {
	h := NewShardedHistory()
	const numGoroutines = 100
	const numOps = 100

	var wg sync.WaitGroup

	// Параллельные записи для разных нод
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		nodeID := "node" + string(rune('A'+i%26))
		go func(node string, start int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				h.Add(node, uint64(start*1000+j+1))
			}
		}(nodeID, i)
	}

	wg.Wait()

	// Проверяем что данные записались
	for i := 0; i < numGoroutines; i++ {
		nodeID := "node" + string(rune('A'+i%26))
		// Проверяем несколько значений
		assert.True(t, h.Has(nodeID, uint64(i*1000+1)))
	}
}

func TestShardedHistory_ConcurrentTryAddRange(t *testing.T) {
	h := NewShardedHistory()
	const numGoroutines = 100

	var wg sync.WaitGroup
	successCount := int64(0)
	var mu sync.Mutex

	// Все горутины пытаются добавить один и тот же range
	r := structs.Range{Start: 1, End: 10}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.TryAddRange("node1", r) {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Только одна горутина должна успешно добавить
	assert.Equal(t, int64(1), successCount)
	assert.True(t, h.HasRange("node1", r))
}

func TestShardedHistory_MergeRanges(t *testing.T) {
	h := NewShardedHistory()

	// Добавляем overlapping ranges
	h.AddRange("node1", structs.Range{Start: 1, End: 5})
	h.AddRange("node1", structs.Range{Start: 3, End: 8})
	h.AddRange("node1", structs.Range{Start: 7, End: 10})

	// Должны слиться в один [1-10]
	assert.True(t, h.HasRange("node1", structs.Range{Start: 1, End: 10}))
	assert.Equal(t, uint64(10), h.ContiguousSeq("node1"))
}

// Benchmark для Advance()-подобной операции
func BenchmarkShardedHistory_Add(b *testing.B) {
	h := NewShardedHistory()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h.Add("local-node", uint64(i+1))
	}
}

func BenchmarkShardedHistory_TryAddRange(b *testing.B) {
	h := NewShardedHistory()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h.TryAddRange("remote-node", structs.Range{Start: uint64(i + 1), End: uint64(i + 1)})
	}
}

func BenchmarkShardedHistory_ParallelAdd(b *testing.B) {
	h := NewShardedHistory()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		nodeID := "node"
		i := 0
		for pb.Next() {
			i++
			h.Add(nodeID, uint64(i))
		}
	})
}

func BenchmarkShardedHistory_ParallelMultiNode(b *testing.B) {
	h := NewShardedHistory()
	nodes := []string{"node1", "node2", "node3", "node4", "node5", "node6", "node7", "node8"}
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			nodeID := nodes[i%len(nodes)]
			i++
			h.Add(nodeID, uint64(i))
		}
	})
}
