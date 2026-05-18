# Key-Based Anti-Entropy Design

## Статус: Draft
## Дата: 2026-03-21

---

## 1. Проблема

Текущий anti-entropy работает по seq-диапазонам:

```
GetVersionVector(peer) → VersionDiff() → Pull(ranges) → peer ищет Update-объекты в Buffer/WAL
```

При обработке `Pull(ranges)` peer ищет Update-объекты сначала в Buffer (`GetCovering`), затем fallback на WAL (`wal.Get(nodeID, seq)`).

**Проблема:** без WAL (noop), при eviction из buffer, Update-объекты теряются навсегда. Нет явного отображения `seq → key` — оно существует только внутри Update-объектов. Если Update evicted из buffer и WAL отключён, peer не может отдать данные по запрошенным seq'ам.

**Следствие:** при отключённой персистентности (noop WAL) anti-entropy становится неработоспособным для данных, вытесненных из buffer.

---

## 2. Решение: Key-Based Anti-Entropy

Переход от range-based к key-based anti-entropy:

1. Для каждого ключа хранится **per-key vector clock** — `map[nodeID]uint64` (последний seq от каждой ноды, менявшей этот ключ)
2. Anti-entropy сравнивает **хеши per-key VC** между нодами
3. Для расходящихся ключей запрашивается **full CRDT state из Engine**

### Ключевой инсайт

Full state берётся из `Engine.Get()` + `CRDT.MarshalJSON()`. **Никакой зависимости от Buffer или WAL.** Существующий `CRDT.Merge()` уже реализован для обоих типов (PNCounter, LWWHLCRegister).

### Почему per-key VC, а не hash CRDT state

| Критерий | Per-key VC | Hash CRDT state |
|----------|-----------|-----------------|
| **Hot path cost** | O(1) — одно присвоение в map | O(N·logN) — serialize + sort + hash |
| **Latency impact** | ~50-100ns (hash малого VC) | микросекунды (serialize CRDT) |
| **Anti-entropy cost** | тяжелее по сети | легче по сети |

Выбран per-key VC, потому что не влияет на latency клиентских операций. Сетевую тяжесть можно оптимизировать позже (Merkle tree).

---

## 3. Новая структура данных: Per-Key Version Clock

Добавляется в `VersionManager`:

```go
type VersionManager struct {
    // ... existing fields ...

    // Per-key version tracking
    keyVersions map[string]map[string]uint64  // key → nodeID → lastSeq
    keyHashes   map[string]uint64             // key → hash(keyVersions[key])
}
```

### Обновление при записи (hot path)

```go
func (vm *VersionManager) Advance(key string) uint64 {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    res := vm.seq.Add(1)
    vm.history.Add(vm.nodeID, res)      // existing
    vm.updateKeyVC(key, vm.nodeID, res) // NEW: O(1)
    return res
}

func (vm *VersionManager) updateKeyVC(key, nodeID string, seq uint64) {
    vc, ok := vm.keyVersions[key]
    if !ok {
        vc = make(map[string]uint64)
        vm.keyVersions[key] = vc
    }
    if seq > vc[nodeID] {
        vc[nodeID] = seq
    }
    vm.keyHashes[key] = hashVC(vc)
}
```

### Hash функция для VC

```go
func hashVC(vc map[string]uint64) uint64 {
    h := fnv.New64a()
    keys := sortedKeys(vc) // 5 нод → сортировка 5 строк
    for _, k := range keys {
        h.Write([]byte(k))
        binary.Write(h, binary.LittleEndian, vc[k])
    }
    return h.Sum64()
}
```

Для 5 нод: сортировка 5 строк + hash ~200 байт ≈ **50-100ns**. На два порядка меньше Engine lock + CRDT modify.

### Обновление при получении remote delta (push path)

В `handleUpdate()`, после успешного apply, добавляется:

```go
vm.updateKeyVC(update.Key, update.NodeID, update.Range.End)
```

---

## 4. Новый протокол Anti-Entropy

### Текущий flow (заменяется)

```
GetVersionVector(peer) → VersionDiff() → Pull(ranges) → serve from Buffer/WAL
```

### Новый flow

```
GetKeyDigests(peer) → CompareDigests() → PullKeyStates(staleKeys) → Merge from Engine
```

### Детальный алгоритм

```go
func (g *DefaultGossip) antiEntropyRound(ctx context.Context) {
    peer := getRandomPeer()

    // Phase 1: Exchange digests — compact: {key → uint64}, ~28 bytes/key
    remoteDigests, err := g.transport.GetKeyDigests(ctx, peer.GossipAddr())
    localDigests := g.versionManager.KeyDigests()

    // Phase 2: Find stale keys
    var staleKeys []string
    for key, remoteHash := range remoteDigests {
        localHash, exists := localDigests[key]
        if !exists || localHash != remoteHash {
            staleKeys = append(staleKeys, key)
        }
    }

    if len(staleKeys) == 0 {
        return // всё синхронно
    }

    // Phase 3: Pull full state for stale keys
    states, err := g.transport.PullKeyStates(ctx, peer.GossipAddr(), staleKeys)

    // Phase 4: Merge
    for _, state := range states {
        g.versionManager.MergeKeyState(ctx, state)
    }
}
```

### Серверная сторона PullKeyStates

```go
func (s *updatesServer) PullKeyStates(ctx context.Context, req *PullKeysRequest) (*PullKeysResponse, error) {
    var states []*KeyStateProto

    for _, key := range req.Keys {
        entry, ok := s.engine.Get(ctx, key)
        if !ok {
            continue
        }

        entry.Mu.RLock()
        stateBytes, _ := entry.Object.MarshalJSON()
        state := &KeyStateProto{
            Key:          key,
            CrdtType:     entry.Object.Type().String(),
            State:        stateBytes,
            Tombstone:    entry.Tombstone,
            SetTimestamp:  marshalTimestamp(entry.SetTimeStamp),
            VersionClock: s.vm.KeyVersionClock(key),
        }
        entry.Mu.RUnlock()

        states = append(states, state)
    }

    return &PullKeysResponse{States: states}, nil
}
```

**Данные берутся из Engine. Никакого Buffer, никакого WAL.**

---

## 5. MergeKeyState — применение full state

```go
func (vm *VersionManager) MergeKeyState(ctx context.Context, state *KeyState) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Десериализуем remote CRDT
    remoteCRDT, err := vm.fabric.New(state.CRDTType, vm.nodeID)
    if err != nil {
        return err
    }
    if err := remoteCRDT.UnmarshalJSON(state.State); err != nil {
        return err
    }

    // Пытаемся обновить существующий entry
    updated, err := vm.engine.Update(ctx, state.Key, func(ctx context.Context, entry *engine.CRDTEntry) (bool, error) {
        // Conflict resolution по SetTimeStamp (аналогично EntryUpdater)
        // Tombstone: побеждает более поздний SetTimeStamp
        if state.Tombstone && !entry.Tombstone && state.SetTimeStamp.After(entry.SetTimeStamp) {
            entry.Tombstone = true
            entry.SetTimeStamp = state.SetTimeStamp
            return true, nil
        }
        if !state.Tombstone && entry.Tombstone && entry.SetTimeStamp.After(state.SetTimeStamp) {
            return false, nil // локальное удаление новее
        }
        if !state.Tombstone && entry.Tombstone && state.SetTimeStamp.After(entry.SetTimeStamp) {
            entry.Tombstone = false
        }

        // CRDT merge — идемпотентен, безопасен
        return true, entry.Object.Merge(remoteCRDT)
    })

    if err != nil {
        return err
    }

    // Ключ не существует локально → создаём
    if !updated {
        vm.engine.PutWithTimeStamp(ctx, state.SetTimeStamp, state.Key, remoteCRDT, func(entry *engine.CRDTEntry) {
            entry.Tombstone = state.Tombstone
        })
    }

    // Обновляем per-key VC (element-wise max)
    vm.mergeKeyVC(state.Key, state.VC)

    return nil
}

func (vm *VersionManager) mergeKeyVC(key string, remoteVC map[string]uint64) {
    localVC, ok := vm.keyVersions[key]
    if !ok {
        localVC = make(map[string]uint64)
        vm.keyVersions[key] = localVC
    }
    for nodeID, seq := range remoteVC {
        if seq > localVC[nodeID] {
            localVC[nodeID] = seq
        }
    }
    vm.keyHashes[key] = hashVC(localVC)
}
```

---

## 6. Изменения интерфейсов

### VersionManager (изменён Advance, добавлены 3 метода)

```go
type VersionManager interface {
    Advance(key string) uint64                                  // CHANGED: добавлен key
    Update(ctx context.Context, updates ...*types.Update) []*types.Update
    VectorClockContiguous() types.VectorClock
    VectorClockMax() types.VectorClock
    VersionDiff(remote types.VectorClock) map[string][]structs.Range
    RestoreFromWal(ctx context.Context, wal wal.WAL) error

    // NEW
    KeyDigests() map[string]uint64                              // {key → hash(VC)}
    KeyVersionClock(key string) map[string]uint64               // VC для конкретного ключа
    MergeKeyState(ctx context.Context, state *KeyState) error   // merge full state
}
```

### Transport (добавлены 2 метода)

```go
type Transport interface {
    Send(ctx context.Context, addr string, data []*types.Update) error
    Pull(ctx context.Context, addr string, versions map[string][]structs.Range) ([]*types.Update, error)
    GetVersion(ctx context.Context, addr string) (types.VectorClock, error)

    // NEW
    GetKeyDigests(ctx context.Context, addr string) (map[string]uint64, error)
    PullKeyStates(ctx context.Context, addr string, keys []string) ([]*KeyState, error)
}
```

### Новый тип KeyState (pkg/types/)

```go
type KeyState struct {
    Key          string
    CRDTType     crdt.CRDTType
    State        []byte              // MarshalJSON() output
    Tombstone    bool
    SetTimeStamp *hlc.Timestamp
    VC           map[string]uint64   // per-key vector clock
}
```

### CRDT interface — без изменений

`Merge()`, `MarshalJSON()`, `UnmarshalJSON()` уже реализованы.

### Engine interface — без изменений

`Get()`, `Update()`, `PutWithTimeStamp()` достаточны.

---

## 7. gRPC Protocol — новые RPC

```protobuf
// Добавить в pkg/transport/grpc/api.proto

message GetKeyDigestsResponse {
    map<string, uint64> digests = 1;     // key → hash(VC)
}

message PullKeyStatesRequest {
    repeated string keys = 1;
}

message KeyStateProto {
    string key = 1;
    string crdt_type = 2;
    bytes state = 3;                     // serialized CRDT
    bool tombstone = 4;
    bytes set_timestamp = 5;             // serialized HLC timestamp
    map<string, uint64> version_clock = 6;
}

message PullKeyStatesResponse {
    repeated KeyStateProto states = 1;
}

service Updates {
    // Existing
    rpc Publish(PublishRequest) returns (google.protobuf.Empty);
    rpc Get(GetRequest) returns (GetResponse);
    rpc GetVersionVector(google.protobuf.Empty) returns (GetVersionVectorResponse);

    // NEW
    rpc GetKeyDigests(google.protobuf.Empty) returns (GetKeyDigestsResponse);
    rpc PullKeyStates(PullKeyStatesRequest) returns (PullKeyStatesResponse);
}
```

---

## 8. Изменения в Storage (callers)

`Advance()` теперь принимает `key`. Все вызовы в `storage.go`:

```go
// Put (line ~199)
seqNum := s.vm.Advance(key)   // was: s.vm.Advance()

// Delete (line ~232)
seqNum := s.vm.Advance(key)

// ApplyInc (line ~274)
seqNum := s.vm.Advance(key)

// ApplyDec (line ~319)
seqNum := s.vm.Advance(key)

// ApplySetRegister (line ~364)
seqNum := s.vm.Advance(key)
```

---

## 9. Изменения в Gossip Server

`updatesServer` получает дополнительную зависимость `engine`:

```go
type updatesServer struct {
    transportpb.UnimplementedUpdatesServer
    vm      version_manager.VersionManager
    buffer  buffer.UpdatesBuffer
    gbuffer *gossip_buffer.GossipBuffer
    wal     wal.WAL
    engine  engine.Engine  // NEW: для PullKeyStates
}
```

---

## 10. Что НЕ меняется

| Компонент | Статус |
|-----------|--------|
| Push path (проактивный gossip) | Без изменений. Buffer → channel → Send → Publish RPC → delta apply. Единственное добавление: `updateKeyVC()` в `handleUpdate()` |
| Buffer (UpdatesBuffer) | Без изменений. Продолжает работать для push-path |
| GossipBuffer (TTL) | Без изменений. Epidemic distribution работает как раньше |
| History (range-based) | Остаётся для push-path дедупликации (`HasRange()`) |
| CRDT типы | Без изменений. `Merge()`, `MarshalJSON()`, `UnmarshalJSON()` уже реализованы |
| Engine | Без изменений |
| Membership (SWIM) | Без изменений |

---

## 11. Оценка ресурсов

### Память (per-key VC + hash)

| Ключей | Нод | keyVersions | keyHashes | Итого |
|--------|-----|-------------|-----------|-------|
| 10K | 5 | ~3.4 MB | ~580 KB | ~4 MB |
| 50K | 5 | ~17 MB | ~2.9 MB | ~20 MB |
| 100K | 5 | ~34 MB | ~5.8 MB | ~40 MB |

### Сеть (anti-entropy раунд, steady state)

| Ключей | Phase 1 (digests) | Phase 2 (stale keys) | Итого |
|--------|------------------|---------------------|-------|
| 10K | ~280 KB | 0 (всё синхронно) | ~280 KB |
| 50K | ~1.4 MB | 0 | ~1.4 MB |
| 100K | ~2.8 MB | 0 | ~2.8 MB |

В steady state (push работает) Phase 2 почти всегда пустая.

### Hot path overhead

~50-100ns на `updateKeyVC()` + `hashVC()` — неизмеримо на фоне CRDT операции + engine lock.

---

## 12. Edge Cases

### Новая нода присоединяется (пустое состояние)

- `KeyDigests()` возвращает пустую map
- Все ключи remote считаются stale → `PullKeyStates(allKeys)`
- Полная синхронизация за один или несколько раундов

### Tombstones

- Tombstoned ключи хранят per-key VC и участвуют в digest exchange
- `MergeKeyState()` обрабатывает конфликты tombstone vs live через `SetTimeStamp`

### Hash collision

- Вероятность: ~1/2^64 на пару ключей — практически невозможно
- Если случится: CRDT merge безопасен (идемпотентен), просто пропустим один раунд синхронизации
- Следующее изменение ключа обновит hash и рассогласование обнаружится

### Большое количество stale keys (после партиции)

- `PullKeyStates` может вернуть много данных
- Решение: лимит на количество ключей за раунд (например, 1000). Остальные подтянутся в следующих раундах

---

## 13. Будущие оптимизации (не MVP)

1. **Merkle tree** поверх key digests — O(log K) вместо O(K) для сравнения
2. **Пагинация** digest exchange для >100K ключей
3. **Incremental digest** — поддержка sorted list для быстрого сравнения
4. **Удаление старых методов** — `VersionDiff()`, `Pull()`, `GetVersionVector()`, `Get()` RPC после полного перехода на key-based anti-entropy