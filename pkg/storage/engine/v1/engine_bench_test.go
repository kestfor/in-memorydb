package v1

import (
	"context"
	"fmt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	"testing"
)

// === Engine Benchmarks ===

func BenchmarkEnginePut(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "benchmark-value"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}
}

func BenchmarkEngineGet(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Pre-populate
	numKeys := 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%numKeys)
		e.Get(ctx, key)
	}
}

func BenchmarkEngineDelete(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Pre-populate
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Delete(ctx, key)
	}
}

func BenchmarkEngineMixed(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	// Pre-populate
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)

		switch i % 3 {
		case 0:
			e.Put(ctx, key, obj, nil)
		case 1:
			e.Get(ctx, key)
		case 2:
			e.Delete(ctx, key)
		}
	}
}

// === Concurrent Benchmarks ===

func BenchmarkEngineConcurrentPut(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "benchmark-value"}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i)
			e.Put(ctx, key, obj, nil)
			i++
		}
	})
}

func BenchmarkEngineConcurrentGet(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Pre-populate
	numKeys := 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%numKeys)
			e.Get(ctx, key)
			i++
		}
	})
}

func BenchmarkEngineConcurrentMixed(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	// Pre-populate
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%1000)

			switch i % 3 {
			case 0:
				e.Put(ctx, key, obj, nil)
			case 1:
				e.Get(ctx, key)
			case 2:
				e.Delete(ctx, key)
			}
			i++
		}
	})
}

// === Sharding Benchmarks ===

func BenchmarkShardFor(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	).(*Engine)
	defer e.Stop()

	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		e.shardFor(keys[i%len(keys)])
	}
}

func BenchmarkShardForConcurrent(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	).(*Engine)
	defer e.Stop()

	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			e.shardFor(keys[i%len(keys)])
			i++
		}
	})
}

// === HLC Benchmarks ===

func BenchmarkHLCNow(b *testing.B) {
	clock := hlc.NewHLC("bench-node")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		clock.Now()
	}
}

func BenchmarkHLCNowConcurrent(b *testing.B) {
	clock := hlc.NewHLC("bench-node")

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			clock.Now()
		}
	})
}

func BenchmarkHLCSyncWithRemote(b *testing.B) {
	clock := hlc.NewHLC("bench-node")
	remote := &hlc.Timestamp{
		WallTime: uint64(b.N),
		Lamport:  100,
		ID:       "remote-node",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		clock.SyncWithRemote(remote)
	}
}

func BenchmarkHLCSyncConcurrent(b *testing.B) {
	clock := hlc.NewHLC("bench-node")
	remote := &hlc.Timestamp{
		WallTime: uint64(b.N),
		Lamport:  100,
		ID:       "remote-node",
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			clock.SyncWithRemote(remote)
		}
	})
}

func BenchmarkTimestampCompare(b *testing.B) {
	ts1 := &hlc.Timestamp{WallTime: 100, Lamport: 50, ID: "node-1"}
	ts2 := &hlc.Timestamp{WallTime: 200, Lamport: 60, ID: "node-2"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		hlc.Compare(ts1, ts2)
	}
}

// === GC Benchmarks ===

func BenchmarkGarbageCollection(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
		WithDeleteThreshold(1), // Very short threshold
	)
	defer e.Stop()

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}

		e.Put(ctx, key, obj, nil)
		e.Delete(ctx, key)
	}
}

// === Memory Benchmarks ===

func BenchmarkMemoryFootprint(b *testing.B) {
	sizes := []int{1000, 10000, 100000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				b.StopTimer()

				e := NewEngine(
					WithInitialShards(256),
					WithNodeID("bench-node"),
				)

				ctx := context.Background()

				b.StartTimer()

				for i := 0; i < size; i++ {
					key := fmt.Sprintf("key-%d", i)
					obj := &MockCRDT{value: fmt.Sprintf("value-%d", i)}
					e.Put(ctx, key, obj, nil)
				}

				b.StopTimer()
				e.Stop()
			}
		})
	}
}

// === Contention Benchmarks ===

func BenchmarkHighContention(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()

	// Все горутины работают с одним ключом
	singleKey := "hotspot-key"
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e.Put(ctx, singleKey, obj, nil)
		}
	})
}

func BenchmarkLowContention(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// Каждая горутина работает со своим набором ключей
			key := fmt.Sprintf("key-%d-%d", b.N, i)
			e.Put(ctx, key, obj, nil)
			i++
		}
	})
}

// === Callback Benchmarks ===

func BenchmarkPutWithCallback(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	callback := func(entry *engine.CRDTEntry) {
		// Simulate some work
		_ = entry.Object
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, callback)
	}
}

func BenchmarkPutWithoutCallback(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}
}

// === Realistic Workload Benchmarks ===

func BenchmarkRealisticWorkload(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	// Pre-populate with some data
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%20000)

			// 70% reads, 25% writes, 5% deletes
			op := i % 100
			switch {
			case op < 70:
				e.Get(ctx, key)
			case op < 95:
				e.Put(ctx, key, obj, nil)
			default:
				e.Delete(ctx, key)
			}
			i++
		}
	})
}

// === Heap Benchmarks ===

func BenchmarkHeapOperations(b *testing.B) {
	heap := newExpiryHeap()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		item := markItem{
			key:      fmt.Sprintf("key-%d", i),
			expiryAt: int64(i * 1000),
		}
		heap.Push(item)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			item := markItem{
				key:      fmt.Sprintf("key-%d", i),
				expiryAt: int64(i * 1000),
			}
			heap.Push(item)
		} else if heap.Len() > 0 {
			heap.Pop()
		}
	}
}

// === Comparative Benchmarks ===

func BenchmarkSmallShards(b *testing.B) {
	e := NewEngine(
		WithInitialShards(4),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}
}

func BenchmarkMediumShards(b *testing.B) {
	e := NewEngine(
		WithInitialShards(256),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}
}

func BenchmarkLargeShards(b *testing.B) {
	e := NewEngine(
		WithInitialShards(1024),
		WithNodeID("bench-node"),
	)
	defer e.Stop()

	ctx := context.Background()
	obj := &MockCRDT{value: "value"}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		e.Put(ctx, key, obj, nil)
	}
}
