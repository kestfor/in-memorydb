# Version Manager v2: План оптимизации для высокой производительности

## Контекст

Lume - распределённое in-memory хранилище на основе CRDT. VersionManager отвечает за:
- Отслеживание sequence numbers локальных операций (`Advance()`)
- Применение updates от remote нод с дедупликацией (`Update()`)
- Предоставление vector clock для anti-entropy синхронизации

**Проблема**: Глобальный `sync.RWMutex` в v1 ограничивает производительность до ~300k QPS при потолке gRPC ~800k QPS.

## Анализ паттернов использования

### Hot paths

1. **Локальные операции** (storage.Put/Delete/ApplyInc/ApplyDec/ApplySetRegister):
   ```
   engine.Put() → vm.Advance() → buffer.Put() → wal.Append()
   ```
   Частота: основной поток запросов (300k+ QPS)

2. **Remote updates** (transport.Publish, gossip.antiEntropyRound):
   ```
   vm.Update(ctx, updates...) → engine.Update() per key
   ```
   Частота: фоновый поток, батчи от нескольких нод

3. **Anti-entropy** (gossip):
   ```
   vm.VectorClockContiguous() → diff → Pull → vm.Update()
   ```
   Частота: периодически (каждые несколько секунд)

### Ключевые наблюдения

1. **Для локальной ноды history не нужен**: `seq` атомарен и всегда contiguous (1, 2, 3...). Мы сами генерируем sequence numbers.

2. **History нужен только для remote нод**: для проверки дубликатов и расчёта diff при anti-entropy.

3. **Updates от разных нод независимы**: можно обрабатывать параллельно.

4. **Updates для разных ключей независимы**: engine имеет per-shard локи (256 шардов).

5. **CRDT операции коммутативны**: порядок применения не влияет на конечный результат.

## Архитектура v2

### Структура

```
pkg/storage/version_manager/v2/
├── version_manager.go         # основной интерфейс без глобального лока
├── version_manager_test.go
├── history/
│   ├── sharded_history.go     # per-node шардированная история
│   └── sharded_history_test.go
└── README.md
```

### Ключевые отличия от v1

| Аспект | v1 | v2 |
|--------|----|----|
| Глобальный лок | `sync.RWMutex` на все операции | Отсутствует |
| `Advance()` | Lock → seq.Add → history.Add → Unlock | `seq.Add()` (атомарно, без history) |
| `Update()` | Lock → loop handleUpdate → Unlock | Параллельно по ключам |
| History для локальной ноды | Хранится | Не хранится (seq атомарен) |
| History для remote | Одна структура под локом | Per-node RWMutex |
| `VectorClockContiguous()` | RLock → iterate all | Атомарный seq + per-node read |

### Новый API

```go
type VersionManager struct {
    nodeID  string
    seq     atomic.Uint64           // локальный sequence, всегда contiguous
    history *ShardedHistory         // только для remote нод
    engine  engine.Engine
    updater *entry_updater.EntryUpdater
}

// Advance - полностью lock-free, O(1)
// History для локальной ноды не нужен - seq атомарен и всегда contiguous
func (vm *VersionManager) Advance() uint64 {
    return vm.seq.Add(1)
}

// Update - параллельная обработка по ключам
func (vm *VersionManager) Update(ctx context.Context, updates ...*types.Update) []*types.Update

// VectorClockContiguous - быстрое чтение без глобального лока
func (vm *VersionManager) VectorClockContiguous() types.VectorClock

// VectorClockMax - аналогично
func (vm *VersionManager) VectorClockMax() types.VectorClock

// VersionDiff - расчёт diff для anti-entropy
func (vm *VersionManager) VersionDiff(remote types.VectorClock) map[string][]structs.Range

// RestoreFromWal - восстановление из WAL
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error
```

## Реализация

### 1. ShardedHistory

Шардированная история с per-node локами:

```go
type NodeHistory struct {
    mu     sync.RWMutex
    ranges []Range  // sorted, merged ranges
}

type ShardedHistory struct {
    mu    sync.RWMutex              // защита map
    nodes map[string]*NodeHistory   // nodeID → history
}

// TryAddRange - атомарная проверка и добавление
// Возвращает true если range был добавлен (не существовал ранее)
func (h *ShardedHistory) TryAddRange(nodeID string, r Range) bool {
    nh := h.getOrCreate(nodeID)

    nh.mu.Lock()
    defer nh.mu.Unlock()

    if containsRange(nh.ranges, r) {
        return false
    }
    insertAndMerge(&nh.ranges, r)
    return true
}

// getOrCreate - double-check locking для создания новой ноды
func (h *ShardedHistory) getOrCreate(nodeID string) *NodeHistory {
    h.mu.RLock()
    nh, ok := h.nodes[nodeID]
    h.mu.RUnlock()
    if ok {
        return nh
    }

    h.mu.Lock()
    defer h.mu.Unlock()
    if nh, ok = h.nodes[nodeID]; ok {
        return nh
    }
    nh = &NodeHistory{}
    h.nodes[nodeID] = nh
    return nh
}

// ContiguousSeq - возвращает contiguous seq для ноды
func (h *ShardedHistory) ContiguousSeq(nodeID string) uint64 {
    h.mu.RLock()
    nh, ok := h.nodes[nodeID]
    h.mu.RUnlock()
    if !ok {
        return 0
    }

    nh.mu.RLock()
    defer nh.mu.RUnlock()
    return calculateContiguous(nh.ranges)
}

// MaxSeq - возвращает max seq для ноды
func (h *ShardedHistory) MaxSeq(nodeID string) uint64

// DiffAll - возвращает missing ranges для всех нод
func (h *ShardedHistory) DiffAll(remote types.VectorClock) map[string][]Range
```

### 2. VersionManager v2

```go
type VersionManager struct {
    nodeID  string
    seq     atomic.Uint64
    history *ShardedHistory
    engine  engine.Engine
    fabric  crdt.CRDTFabric
    updater *entry_updater.EntryUpdater
}

func NewVersionManager(nodeID string, engine engine.Engine) *VersionManager {
    return &VersionManager{
        nodeID:  nodeID,
        history: NewShardedHistory(),
        engine:  engine,
        fabric:  crdt.NewFabric(),
        updater: entry_updater.NewEntryUpdater(crdt.NewFabric(), nodeID),
    }
}

// Advance - O(1), полностью lock-free
func (vm *VersionManager) Advance() uint64 {
    return vm.seq.Add(1)
}

// Update - параллельная обработка
func (vm *VersionManager) Update(ctx context.Context, updates ...*types.Update) []*types.Update {
    if len(updates) == 0 {
        return nil
    }

    // Для небольших батчей - последовательно
    if len(updates) < 10 {
        return vm.updateSequential(ctx, updates)
    }

    // Для больших батчей - параллельно по ключам
    return vm.updateParallel(ctx, updates)
}

func (vm *VersionManager) updateSequential(ctx context.Context, updates []*types.Update) []*types.Update {
    applied := make([]*types.Update, 0, len(updates))

    for _, update := range updates {
        // TryAddRange атомарно для одной ноды
        if !vm.history.TryAddRange(update.NodeID, update.Range) {
            continue // дубликат
        }

        // Применяем к engine (engine имеет per-shard локи)
        if vm.applyUpdate(ctx, update) {
            applied = append(applied, update)
        }
    }

    return applied
}

func (vm *VersionManager) updateParallel(ctx context.Context, updates []*types.Update) []*types.Update {
    // Используем worker pool для параллельной обработки
    const numWorkers = 8

    type result struct {
        update  *types.Update
        applied bool
    }

    jobs := make(chan *types.Update, len(updates))
    results := make(chan result, len(updates))

    // Запускаем workers
    var wg sync.WaitGroup
    for i := 0; i < numWorkers; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for update := range jobs {
                if !vm.history.TryAddRange(update.NodeID, update.Range) {
                    results <- result{update, false}
                    continue
                }
                applied := vm.applyUpdate(ctx, update)
                results <- result{update, applied}
            }
        }()
    }

    // Отправляем задания
    for _, update := range updates {
        jobs <- update
    }
    close(jobs)

    // Ждём завершения и собираем результаты
    go func() {
        wg.Wait()
        close(results)
    }()

    var applied []*types.Update
    for r := range results {
        if r.applied {
            applied = append(applied, r.update)
        }
    }

    return applied
}

func (vm *VersionManager) applyUpdate(ctx context.Context, update *types.Update) bool {
    // Логика из v1 handleUpdate, но без глобального лока
    // Engine уже имеет per-shard локи
    // ...
}

// VectorClockContiguous - без глобального лока
func (vm *VersionManager) VectorClockContiguous() types.VectorClock {
    vc := vm.history.AllContiguousSeq()
    vc[vm.nodeID] = vm.seq.Load()  // для локальной ноды - атомарный seq
    return vc
}

// VectorClockMax - аналогично
func (vm *VersionManager) VectorClockMax() types.VectorClock {
    vc := vm.history.AllMaxSeq()
    vc[vm.nodeID] = vm.seq.Load()
    return vc
}

// VersionDiff - делегирует в history
func (vm *VersionManager) VersionDiff(remote types.VectorClock) map[string][]structs.Range {
    return vm.history.DiffAll(remote)
}

// RestoreFromWal - восстановление, можно параллелить
func (vm *VersionManager) RestoreFromWal(ctx context.Context, wal wal.WAL) error {
    var localCount uint64

    err := wal.ReplayAll(ctx, func(u *types.Update) error {
        if u.NodeID == vm.nodeID {
            localCount++
        } else {
            // Для remote - добавляем в history и применяем
            vm.history.TryAddRange(u.NodeID, u.Range)
        }
        vm.applyUpdate(ctx, u)
        return nil
    })

    if err != nil {
        return err
    }

    // Устанавливаем seq для локальной ноды
    vm.seq.Store(localCount)
    return nil
}
```

## Гарантии корректности

### Инварианты

1. **seq для локальной ноды всегда contiguous**: мы сами генерируем через Advance()
2. **Дедупликация**: TryAddRange атомарна для каждой ноды
3. **CRDT consistency**: операции коммутативны, порядок не важен
4. **Thread-safety engine**: engine имеет per-shard локи

### Race conditions и их предотвращение

| Сценарий | Решение |
|----------|---------|
| Два потока создают NodeHistory | Double-check locking в getOrCreate() |
| Два потока применяют один update | TryAddRange() атомарна |
| Чтение VectorClock во время записи | Per-node RWMutex |
| Запись в engine из нескольких потоков | Engine per-shard локи |

## Файлы для создания/изменения

### Новые файлы

1. `pkg/storage/version_manager/v2/version_manager.go`
2. `pkg/storage/version_manager/v2/version_manager_test.go`
3. `pkg/storage/version_manager/v2/history/sharded_history.go`
4. `pkg/storage/version_manager/v2/history/sharded_history_test.go`
5. `pkg/storage/version_manager/v2/README.md`

### Изменения в существующих файлах

1. `cmd/grpc/app/subsystems.go` - переключить на v2

## Порядок реализации

1. Создать структуру директорий v2
2. Реализовать ShardedHistory с per-node локами
3. Реализовать VersionManager v2
4. Переиспользовать entry_updater из v1
5. Добавить unit тесты
6. Добавить concurrent тесты
7. Обновить subsystems.go для использования v2
8. Запустить существующие интеграционные тесты
9. Провести нагрузочное тестирование

## Ожидаемые результаты

| Операция | v1 | v2 | Улучшение |
|----------|----|----|-----------|
| Advance() | Lock + history.Add | atomic.Add (O(1)) | 10-100x |
| Update() (один ключ) | Lock весь батч | Per-node lock | 2-5x |
| Update() (много ключей) | Lock весь батч | Parallel workers | 5-10x |
| VectorClockContiguous() | RLock all | Per-node RLock | 2-3x |

**Целевой QPS**: 600-800k (2-3x улучшение от текущих 300k)

## Дополнительные оптимизации (опционально)

1. **Worker pool переиспользование**: глобальный пул для Update()
2. **Batch WAL writes**: группировать записи в WAL
3. **Кеширование VectorClock**: при частых чтениях
4. **Lock-free history**: использование atomic операций вместо RWMutex

## Метрики для мониторинга

- QPS по операциям (Advance, Update, VectorClock)
- Latency p50/p95/p99
- Lock contention (если остаётся)
- Goroutine count при параллельном Update