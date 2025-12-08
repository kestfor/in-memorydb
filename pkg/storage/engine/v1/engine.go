package v1

import (
	"container/list"
	"context"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/crdt/hlc"
	"in-memorydb/pkg/observability/tracing"
	"in-memorydb/pkg/storage/engine"
	"in-memorydb/pkg/structs"
	"in-memorydb/pkg/utils"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const defaultInitialShards = 256
const defaultDeleteThreshold = time.Minute
const markChanSize = 10000 // буфер для канала

type shard struct {
	mu   sync.RWMutex
	data map[string]*engine.CRDTEntry
}

type markItem struct {
	key          string
	setTimeStamp *hlc.Timestamp
	expiryAt     int64
}

type Engine struct {
	nodeID    string
	shards    atomic.Pointer[[]*shard]
	numShards atomic.Uint32
	clock     *hlc.Time

	opts Options

	fallbackMu sync.Mutex
	fallback   *list.List
	markChan   chan markItem

	// для graceful shutdown
	gcCancel context.CancelFunc
	gcWg     sync.WaitGroup
}

type Options struct {
	InitialShards   int
	NodeID          string
	DeleteThreshold time.Duration
}

type Option func(*Options)

func WithInitialShards(initialShards int) Option {
	return func(o *Options) {
		o.InitialShards = initialShards
	}
}

func WithNodeID(nodeID string) Option {
	return func(o *Options) {
		o.NodeID = nodeID
	}
}

func WithDeleteThreshold(deleteThreshold time.Duration) Option {
	return func(o *Options) {
		o.DeleteThreshold = deleteThreshold
	}
}

func populateDefaults(opts *Options) {
	if opts.InitialShards == 0 {
		opts.InitialShards = defaultInitialShards
	}
	if opts.DeleteThreshold == 0 {
		opts.DeleteThreshold = defaultDeleteThreshold
	}
}

func NewEngine(options ...Option) engine.Engine {
	opts := Options{}
	for _, option := range options {
		option(&opts)
	}

	populateDefaults(&opts)

	e := &Engine{
		opts:     opts,
		clock:    hlc.NewHLC(opts.NodeID),
		fallback: list.New(),
		markChan: make(chan markItem, markChanSize),
	}

	initial := opts.InitialShards
	arr := make([]*shard, initial)
	for i := 0; i < initial; i++ {
		arr[i] = &shard{
			data: make(map[string]*engine.CRDTEntry, 128),
		}
	}
	e.shards.Store(&arr)
	e.numShards.Store(uint32(initial))

	return e
}

// Start initiates the garbage collection process for the engine by starting a background goroutine.
func (e *Engine) Start(ctx context.Context) error {
	ctx, e.gcCancel = context.WithCancel(ctx)
	e.gcWg.Add(1)
	go func() {
		defer e.gcWg.Done()
		e.runGC(ctx)
	}()
	return nil
}

// Stop - graceful shutdown
func (e *Engine) Stop() {
	if e.gcCancel == nil {
		return
	}
	e.gcCancel()
	e.gcWg.Wait()
	close(e.markChan)
}

func (e *Engine) Get(ctx context.Context, key string) (*engine.CRDTEntry, bool) {
	_, span := tracing.StartSpan(ctx, "engine.Get", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	shard := e.shardFor(key)
	shard.mu.RLock()
	defer shard.mu.RUnlock()
	entry, ok := shard.data[key]

	if !ok {
		return nil, false
	}

	if entry.Tombstone {
		return nil, false
	}

	return entry, true
}

func (e *Engine) Put(ctx context.Context, key string, obj crdt.CRDT, callback engine.Callback) *hlc.Timestamp {
	return e.PutWithTimeStamp(ctx, e.Clock().Now(), key, obj, callback)
}

func (e *Engine) PutWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string, obj crdt.CRDT, callback engine.Callback) *hlc.Timestamp {
	_, span := tracing.StartSpan(ctx, "engine.PutWithTimeStamp", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	shard := e.shardFor(key)

	shard.mu.Lock()

	val, ok := shard.data[key]

	if !ok {
		val = &engine.CRDTEntry{}
	}

	val.Object = obj
	val.SetTimeStamp = ts.Copy()
	val.Tombstone = false

	if !ok {
		shard.data[key] = val
	}

	shard.mu.Unlock()

	if callback != nil {
		callback(val)
	}

	return ts
}

func (e *Engine) Clock() *hlc.Time {
	return e.clock
}

// Delete removes the specified key from the engine by marking it as a tombstone and scheduling it for garbage collection.
func (e *Engine) Delete(ctx context.Context, key string) (*engine.CRDTEntry, bool) {
	return e.DeleteWithTimeStamp(ctx, e.clock.Now(), key)
}

// DeleteWithTimeStamp marks a key as a tombstone with a specified timestamp and schedules it for garbage collection.
func (e *Engine) DeleteWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string) (*engine.CRDTEntry, bool) {
	sh := e.shardFor(key)
	sh.mu.Lock()
	ent, ok := sh.data[key]

	if !ok {
		sh.mu.Unlock()
		return nil, false
	}

	ent.Tombstone = true
	ent.SetTimeStamp = ts.Copy()
	sh.mu.Unlock()

	item := markItem{
		setTimeStamp: ts.Copy(),
		key:          key,
		expiryAt:     time.Now().UnixNano() + int64(e.opts.DeleteThreshold),
	}

	select {
	case e.markChan <- item:
	default:
		// Канал полон, используем fallback
		e.fallbackMu.Lock()
		e.fallback.PushBack(item)
		e.fallbackMu.Unlock()
	}
	return ent, true
}

// Update выполняет atomic update entry
func (e *Engine) Update(ctx context.Context, key string, updateFunc engine.UpdateFunc) (modified bool, err error) {
	sh := e.shardFor(key)
	sh.mu.Lock()

	ent, ok := sh.data[key]
	if !ok {
		sh.mu.Unlock()
		return false, nil
	}

	ent.Mu.Lock()
	sh.mu.Unlock()
	defer ent.Mu.Unlock()

	return updateFunc(ctx, ent)
}

// GetOrCreate получает существующий entry или создаёт новый
func (e *Engine) GetOrCreate(ctx context.Context, key string, createFunc engine.CreateFunc) (*engine.CRDTEntry, bool, error) {
	sh := e.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	// Проверяем существование
	if ent, ok := sh.data[key]; ok && !ent.Tombstone {
		return ent, false, nil
	}

	// Создаём новый entry
	newEntry, err := createFunc(ctx)
	if err != nil {
		return nil, false, err
	}

	sh.data[key] = newEntry

	return newEntry, true, nil
}

func (e *Engine) runGC(ctx context.Context) {
	heap := newExpiryHeap()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case item := <-e.markChan:
			heap.Push(item)

		case <-ticker.C:
			// Периодически drain fallback
			e.drainFallback(heap)

			// Обрабатываем expired items
			e.processExpiredItems(heap)
		}
	}
}

func (e *Engine) drainFallback(heap *expiryHeap) {
	e.fallbackMu.Lock()
	for it := e.fallback.Front(); it != nil; {
		heap.Push(it.Value.(markItem))
		old := it
		it = it.Next()
		e.fallback.Remove(old)
	}
	e.fallbackMu.Unlock()
}

// TODO здесь какая-то шиза с кучей происходит, peek и pop смотрят на разные элементы
func (e *Engine) processExpiredItems(heap *expiryHeap) {
	currTime := time.Now().UnixNano()
	deletedKeys := structs.NewSet[string]()
	for {

		item, ok := heap.Peek()
		if !ok || item.expiryAt > currTime {
			break
		}

		item = heap.Pop().(markItem)

		sh := e.shardFor(item.key)
		sh.mu.Lock()
		cur, exists := sh.data[item.key]

		if exists && cur != nil && cur.Tombstone && cur.SetTimeStamp.Equal(item.setTimeStamp) {
			deletedKeys.Add(item.key)
			delete(sh.data, item.key)
		}

		sh.mu.Unlock()
	}
	if len(deletedKeys) > 0 {
		slog.Debug("engine.processExpiredItems: Items processed", "deleted", deletedKeys.Slice())
	}
}

func (e *Engine) shardFor(key string) *shard {
	arr := *e.shards.Load()
	n := int(e.numShards.Load())
	idx := int(utils.HashKey(key) & uint64(n-1))
	return arr[idx]
}
