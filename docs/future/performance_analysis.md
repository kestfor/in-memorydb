# Анализ производительности in-memorydb

## Дата анализа: 10 декабря 2025

---

## Обзор архитектуры

Проект представляет собой распределённую in-memory базу данных с поддержкой:
- **CRDT типов** (LWW Register с HLC, PN Counter)
- **Gossip протокола** для репликации данных между нодами
- **Write-Ahead Log (WAL)** для персистентности
- **Sharded Engine** для эффективного параллельного доступа
- **Version Manager** с History для отслеживания обновлений
- **gRPC API** для клиентского доступа

---

## Сильные стороны

### 1. Продуманная архитектура шардирования
- **256 shardов по умолчанию** с возможностью настройки
- Каждый shard имеет собственный `sync.RWMutex`, минимизируя contention
- Использование FNV-1a для хеширования ключей — детерминированное и быстрое

### 2. HLC (Hybrid Logical Clock) для CRDT
- Корректная реализация HLC с `atomic.Pointer` для lock-free чтения
- Поддержка `offset` для синхронизации времени между нодами
- Грамотное сравнение timestamp с учётом WallTime, Lamport и NodeID

### 3. Эффективный Updates Buffer v2
- LRU-буфер с тремя индексами для O(log n) поиска
- Слияние смежных диапазонов для экономии памяти
- Поддержка `collapseUpdates` для объединения consecutive updates

### 4. Garbage Collection с неблокирующим дизайном
- Использование канала `markChan` для tombstone пометок
- Fallback на list при переполнении канала
- Min-heap для эффективного eviction по времени

### 5. Gossip с адаптивным TTL
- Формула TTL учитывает размер кластера и fanout
- Anti-entropy раунды для eventual consistency
- Gossip Buffer с circular buffer для epidemic распространения

### 6. Полноценный трейсинг (OpenTelemetry)
- Spans для всех критических операций
- Атрибуты для диагностики (node_id, key, update_count)
- Интеграция с Tempo/Jaeger

---

## Слабые стороны

### 1. JSON-сериализация в критических путях
- **Проблема:** `encoding/json` используется в WAL (`wal.Append`), Update маршалинге
- **Влияние:** Высокая нагрузка на аллокатор, ~3-5x медленнее binary форматов
- **Локация:** `pkg/storage/wal/v1/wal.go:74-80`, `pkg/types/update.go`

### 2. FNV-1a хеширование создаёт объект на каждый вызов
- **Проблема:** `fnv.New64a()` аллоцирует hasher на каждый `HashKey()`
- **Влияние:** GC pressure при высоком RPS
- **Локация:** `pkg/utils/utils.go:7-10`

### 3. Глобальный мьютекс в VersionManager
- **Проблема:** `sync.RWMutex` блокирует все операции Update/VectorClock
- **Влияние:** Bottleneck при многих конкурентных обновлениях
- **Локация:** `pkg/storage/version_manager/v1/version_manager.go:30`

### 4. Copy timestamp при каждом PutWithTimeStamp
- **Проблема:** `ts.Copy()` создаёт новый `Timestamp` объект
- **Влияние:** Избыточные аллокации при write-heavy нагрузке
- **Локация:** `pkg/storage/engine/v1/engine.go:173-174`

### 5. Sync.Pool не используется для переиспользования объектов
- **Проблема:** `&types.Update{}`, `&engine.CRDTEntry{}` создаются на каждую операцию
- **Влияние:** Частые вызовы GC при высоком RPS
- **Локация:** `pkg/storage/storage.go:220-228`

### 6. WAL записывает каждый seq отдельно
- **Проблема:** Цикл `for i := u.Range.Start; i <= u.Range.End; i++` в `Append`
- **Влияние:** N syscalls вместо 1 при batch update
- **Локация:** `pkg/storage/wal/v1/wal.go:85-90`

### 7. Отсутствие connection pooling в gRPC транспорте
- **Проблема:** Возможно создание нового connection на каждый Send/Pull
- **Влияние:** Overhead на TLS handshake и connection setup
- **Локация:** `pkg/transport/grpc/`

### 8. History хранит отсортированный slice вместо интервального дерева
- **Проблема:** `insertAndMerge` имеет O(n) worst-case при слиянии
- **Влияние:** Деградация при большом количестве gaps в seq numbers
- **Локация:** `pkg/storage/version_manager/v1/history/history.go:42-89`

---

## План атомарных оптимизаций

Каждая оптимизация ниже **независима** и может быть реализована отдельно.
Оптимизации упорядочены по ожидаемому влиянию на производительность.

---

### OPT-001: Замена encoding/json на json-iterator в WAL

**Приоритет:** Высокий  
**Сложность:** Низкая  
**Ожидаемый эффект:** -40-60% CPU на сериализации, -50% аллокаций в WAL path

**Описание:**
Заменить `encoding/json` на `github.com/json-iterator/go` с ConfigFastest.

**Изменяемые файлы:**
- `pkg/storage/wal/v1/wal.go`
- `pkg/types/update.go`

**Код изменения:**
```go
import jsoniter "github.com/json-iterator/go"

var json = jsoniter.ConfigFastest

// Использование остаётся тем же: json.Marshal(), json.Unmarshal()
```

**Тестирование:**
- Benchmark: `go test -bench=. ./pkg/storage/wal/v1/`
- Проверить корректность: существующие тесты должны проходить

---

### OPT-002: Использование xxhash вместо FNV-1a для HashKey

**Приоритет:** Средний  
**Сложность:** Низкая  
**Ожидаемый эффект:** -50% времени хеширования, zero allocation

**Описание:**
Заменить `hash/fnv` на `github.com/cespare/xxhash/v2` который не аллоцирует.

**Изменяемые файлы:**
- `pkg/utils/utils.go`

**Код изменения:**
```go
import "github.com/cespare/xxhash/v2"

func HashKey(key string) uint64 {
    return xxhash.Sum64String(key)
}
```

**Тестирование:**
- Benchmark: `go test -bench=BenchmarkHashKey ./pkg/utils/`
- Проверить распределение: коллизии не должны увеличиться

---

### OPT-003: sync.Pool для Update объектов

**Приоритет:** Высокий  
**Сложность:** Средняя  
**Ожидаемый эффект:** -30% аллокаций на write path

**Описание:**
Добавить `sync.Pool` для переиспользования `*types.Update` объектов.

**Изменяемые файлы:**
- `pkg/types/update.go` (добавить pool)
- `pkg/storage/storage.go` (использовать pool)

**Код изменения:**
```go
// pkg/types/update.go
var updatePool = sync.Pool{
    New: func() interface{} {
        return &Update{}
    },
}

func AcquireUpdate() *Update {
    return updatePool.Get().(*Update)
}

func ReleaseUpdate(u *Update) {
    *u = Update{} // reset
    updatePool.Put(u)
}
```

**Тестирование:**
- Benchmark: `go test -bench=BenchmarkPut ./pkg/storage/`
- Memory profile: `go test -memprofile=mem.out`

---

### OPT-004: Sharded locks в VersionManager по nodeID

**Приоритет:** Высокий  
**Сложность:** Средняя  
**Ожидаемый эффект:** -60% contention при multi-node updates

**Описание:**
Вместо единого `sync.RWMutex` использовать map с мьютексами по nodeID в History.

**Изменяемые файлы:**
- `pkg/storage/version_manager/v1/history/history.go`
- `pkg/storage/version_manager/v1/version_manager.go`

**Код изменения:**
```go
// pkg/storage/version_manager/v1/history/history.go
type NodeHistory struct {
    mu     sync.RWMutex // per-node lock
    Ranges []Range
}

func (h *History) AddRange(node string, r Range) {
    nh := h.getOrCreate(node)
    nh.mu.Lock()
    defer nh.mu.Unlock()
    insertAndMerge(&nh.Ranges, r)
}
```

**Тестирование:**
- Benchmark: `go test -bench=BenchmarkUpdate -cpu=1,2,4,8 ./pkg/storage/version_manager/v1/`

---

### OPT-005: Batch write в WAL

**Приоритет:** Высокий  
**Сложность:** Средняя  
**Ожидаемый эффект:** -70% syscalls при batch updates

**Описание:**
Добавить метод `AppendBatch` для записи нескольких updates одним вызовом.

**Изменяемые файлы:**
- `pkg/storage/wal/wal.go` (интерфейс)
- `pkg/storage/wal/v1/wal.go` (реализация)

**Код изменения:**
```go
// Интерфейс
type WAL interface {
    Append(ctx context.Context, u *types.Update) error
    AppendBatch(ctx context.Context, updates []*types.Update) error
    // ...
}

// Реализация с буферизацией
func (ww *walWrapper) AppendBatch(ctx context.Context, updates []*types.Update) error {
    // Сериализовать все updates один раз
    // Записать в WAL одним batch
}
```

**Тестирование:**
- Benchmark: `go test -bench=BenchmarkAppendBatch ./pkg/storage/wal/v1/`

---

### OPT-006: Избежать Copy() для immutable timestamps

**Приоритет:** Низкий  
**Сложность:** Низкая  
**Ожидаемый эффект:** -10-15% аллокаций на write path

**Описание:**
В методах где timestamp не модифицируется после создания, избежать `ts.Copy()`.

**Изменяемые файлы:**
- `pkg/storage/engine/v1/engine.go`

**Код изменения:**
```go
// Если ts гарантированно immutable после Now(), можно:
func (e *Engine) PutWithTimeStamp(ctx context.Context, ts *hlc.Timestamp, key string, obj crdt.CRDT, callback engine.Callback) *hlc.Timestamp {
    // ...
    val.SetTimeStamp = ts // без Copy, если caller не модифицирует ts
    // ...
}
```

**Тестирование:**
- Race detector: `go test -race ./pkg/storage/engine/v1/`

---

### OPT-007: Connection pool в gRPC Transport

**Приоритет:** Средний  
**Сложность:** Средняя  
**Ожидаемый эффект:** -80% latency на первый запрос к каждой ноде

**Описание:**
Кэшировать gRPC connections по адресу с TTL для переиспользования.

**Изменяемые файлы:**
- `pkg/transport/grpc/transport.go`

**Код изменения:**
```go
type GRPCTransport struct {
    // ...
    connPool sync.Map // addr -> *grpc.ClientConn
    // ...
}

func (t *GRPCTransport) getConn(addr string) (*grpc.ClientConn, error) {
    if conn, ok := t.connPool.Load(addr); ok {
        return conn.(*grpc.ClientConn), nil
    }
    // создать новый и сохранить
}
```

**Тестирование:**
- Integration test с несколькими нодами
- Проверить cleanup при закрытии

---

### OPT-008: Interval Tree вместо sorted slice в History

**Приоритет:** Низкий  
**Сложность:** Высокая  
**Ожидаемый эффект:** O(log n) insert вместо O(n) worst-case

**Описание:**
Заменить `[]Range` на интервальное дерево для эффективного слияния.

**Изменяемые файлы:**
- `pkg/storage/version_manager/v1/history/history.go`

**Код изменения:**
```go
// Использовать github.com/biogo/store/interval или собственную реализацию
type NodeHistory struct {
    tree *interval.Tree
}
```

**Тестирование:**
- Benchmark с 10000+ gaps: `go test -bench=BenchmarkAddRangeMany`

---

### OPT-009: Предаллокация slices в критических путях

**Приоритет:** Низкий  
**Сложность:** Низкая  
**Ожидаемый эффект:** -5-10% аллокаций

**Описание:**
Использовать `make([]T, 0, expectedCap)` вместо `var result []T`.

**Изменяемые файлы:**
- `pkg/storage/version_manager/v1/version_manager.go:68`
- `pkg/gossip/gossip/gossip.go`
- `pkg/transport/grpc/server.go`

**Код изменения:**
```go
// Было:
applied := make([]*types.Update, 0, len(updates)/2+1)

// Убедиться что везде используется capacity hint
result := make([]byte, 0, estimatedSize)
```

**Тестирование:**
- Memory profile: `go tool pprof -alloc_space`

---

### OPT-010: Использование protobuf для внутренней сериализации

**Приоритет:** Средний  
**Сложность:** Высокая  
**Ожидаемый эффект:** -60% размер данных, -50% CPU на сериализации

**Описание:**
Унифицировать сериализацию WAL/buffer с protobuf вместо JSON.

**Изменяемые файлы:**
- `api/lume.proto` (добавить Update message)
- `pkg/storage/wal/v1/wal.go`
- `pkg/types/update.go`

**Тестирование:**
- Миграция существующих WAL файлов
- Backward compatibility тесты

---

## Метрики для отслеживания

После каждой оптимизации измерять:

1. **Throughput:** ops/sec для Put/Get/Update
2. **Latency:** p50, p95, p99 для каждой операции
3. **Memory:** allocs/op, bytes/op
4. **GC pressure:** pause time, frequency
5. **CPU profile:** top 10 functions by CPU time

### Benchmark команды

```bash
# Общий benchmark
go test -bench=. -benchmem ./pkg/...

# CPU profiling
go test -bench=BenchmarkPut -cpuprofile=cpu.out ./pkg/storage/
go tool pprof -http=:8080 cpu.out

# Memory profiling
go test -bench=BenchmarkPut -memprofile=mem.out ./pkg/storage/
go tool pprof -http=:8080 mem.out

# Trace
go test -bench=BenchmarkPut -trace=trace.out ./pkg/storage/
go tool trace trace.out
```

---

## Рекомендуемый порядок внедрения

1. **OPT-001** (json-iterator) — максимальный эффект при минимальных изменениях
2. **OPT-003** (sync.Pool для Update) — снижение GC pressure
3. **OPT-005** (batch WAL) — критично для write-heavy нагрузки
4. **OPT-004** (sharded VM locks) — снижение contention
5. **OPT-002** (xxhash) — простое улучшение
6. **OPT-007** (connection pool) — важно для распределённых сценариев
7. **OPT-006** (immutable ts) — minor optimization
8. **OPT-009** (preallocation) — minor optimization
9. **OPT-008** (interval tree) — если History становится bottleneck
10. **OPT-010** (protobuf) — если JSON остаётся проблемой после OPT-001

---

## Заключение

Текущая архитектура проекта хорошо продумана и масштабируема. Основные bottlenecks связаны с:
- JSON сериализацией (легко исправить)
- Глобальными locks (требует рефакторинга)
- Избыточными аллокациями (sync.Pool)

Предложенные оптимизации атомарны и могут внедряться независимо, постепенно улучшая производительность без риска регрессии.

