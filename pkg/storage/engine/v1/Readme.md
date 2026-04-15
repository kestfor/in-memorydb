# Engine v1 — Static Sharded Architecture

## 🚀 Особенности

- **In-memory** хранилище с CRDT-объектами
- **Статичное количество шардов**, выбираемое при старте
- Полная **конкурентность** благодаря per-shard RWMutex
- **Hybrid Logical Clock (HLC)** для версионирования
- **Tombstone + GC** для корректного удаления
- **Детерминированная**, простая и безопасная архитектура (без миграции/ресайза)
---

## 🧱 Архитектура

### Структура шардов

Движок использует статичный массив шардов:

```
shards: []*shard
numShards: uint32
```

Каждый `shard` содержит:

```go
type shard struct {
    mu   sync.RWMutex
    data map[string]*CRDTEntry
}
```

### Выбор шарда

```
idx = Hash(key) & (numShards - 1)
```

Это быстрый O(1) способ распределения ключей по шардовым сегментам.

### Инициализация

При запуске:

```go
arr := make([]*shard, initialShards)
for i := range arr {
    arr[i] = &shard{data: make(map[string]*CRDTEntry)}
}
```

**Количество шардов больше не меняется**, что полностью исключает гонки миграции.

---

## 🔍 Операции

### Get(key)

1. Определяем шард по хешу
2. Берём `RLock`
3. Читаем значение
4. Если ключ не найден или tombstone `false`
5. Может вернуть entry, помеченную для удаления, для этого есть метод проверки у самой entry

### Put(key, value)

1. Определяем шард
2. `Lock`
3. Записываем CRDTEntry с HLC timestamp
4. Инкрементируем `countKeys` для новых ключей

### Delete(key)

Помечает ключ tombstone-флагом и отправляет его в GC очередь для будущего удаления.

---

## ⏳ GC (удаление tombstone)

Фоновая горутина:

- принимает tombstone-записи через канал `markChan`
- складывает их в min-heap по `expiryTime`
- периодически (каждые 100ms):
    - очищает истекшие tombstone
    - удаляет физически из соответствующего шарда

GC работает только с RWMutex конкретного шарда, не блокируя весь движок.

---

## 🕒 Hybrid Logical Clock (HLC)

Каждая запись имеет timestamp:

```
SetTimeStamp *hlc.Timestamp
```

Используется для:

- монотонного упорядочивания изменений
- корректного сравнения версий CRDT

---

## 🔐 Конкурентная модель

- Нет глобальных блокировок
- Каждый шард живёт под своим RWMutex
- Параллельные Put/Get/Delete на разных шардов не блокируют друг друга
- Даже под `go test -race` нет гонок

---

## ⚠ Ограничения

- Количество шардов *фиксировано* и задаётся через `WithInitialShards`.
- Изменить количество шардов без перезапуска невозможно.
- Масштабирование производительности достигается увеличением количества шардов при старте.

---

## 🧪 Пример использования

```go
e := NewEngine(
    WithInitialShards(256),
    WithNodeID("node-1"),
)

e.Start()
defer e.Stop()

ts := e.Put(ctx, "key-123", &MyCRDT{}, nil)

val, ok := e.Get(ctx, "key-123")
```

---

## 🎯 Итоги

Эта реализация:

- проста
- надёжна
- хорошо параллелится
- не содержит миграции и опасных состояний
- обеспечивает высокую производительность и предсказуемость

## Результаты benchmark тестов
В benchmark-выводе теперь дополнительно репортится метрика `entities/s`, чтобы вместе с `ns/op` было сразу видно throughput.

```
goos: windows
goarch: amd64
pkg: in-memorydb/pkg/storage/engine/v1
cpu: AMD Ryzen 7 5800H with Radeon Graphics         
BenchmarkEnginePut
BenchmarkEnginePut-16                	 1234209	       846.8 ns/op	     391 B/op	      10 allocs/op
BenchmarkEngineGet
BenchmarkEngineGet-16                	 3366634	       347.9 ns/op	     183 B/op	       6 allocs/op
BenchmarkEngineDelete
BenchmarkEngineDelete-16             	 2538212	       482.3 ns/op	     151 B/op	       5 allocs/op
BenchmarkEngineMixed
BenchmarkEngineMixed-16              	 2906478	       389.8 ns/op	     205 B/op	       7 allocs/op
BenchmarkEngineConcurrentPut
BenchmarkEngineConcurrentPut-16      	 4609845	       259.7 ns/op	     373 B/op	      14 allocs/op
BenchmarkEngineConcurrentGet
BenchmarkEngineConcurrentGet-16      	14384418	        83.53 ns/op	     183 B/op	       6 allocs/op
BenchmarkEngineConcurrentMixed
BenchmarkEngineConcurrentMixed-16    	 4565403	       339.4 ns/op	     225 B/op	       9 allocs/op
BenchmarkShardFor
BenchmarkShardFor-16                 	196925823	         6.046 ns/op	       0 B/op	       0 allocs/op
BenchmarkShardForConcurrent
BenchmarkShardForConcurrent-16       	1000000000	         1.005 ns/op	       0 B/op	       0 allocs/op
BenchmarkHLCNow
BenchmarkHLCNow-16                   	19186737	        62.17 ns/op	      48 B/op	       2 allocs/op
BenchmarkHLCNowConcurrent
BenchmarkHLCNowConcurrent-16         	 4786501	       261.5 ns/op	     132 B/op	       7 allocs/op
BenchmarkHLCSyncWithRemote
BenchmarkHLCSyncWithRemote-16        	18226002	        63.16 ns/op	      48 B/op	       2 allocs/op
BenchmarkHLCSyncConcurrent
BenchmarkHLCSyncConcurrent-16        	 4343048	       265.7 ns/op	     132 B/op	       7 allocs/op
BenchmarkTimestampCompare
BenchmarkTimestampCompare-16         	1000000000	         0.5237 ns/op	       0 B/op	       0 allocs/op
BenchmarkGarbageCollection
BenchmarkGarbageCollection-16        	 1000000	      1471 ns/op	     579 B/op	      16 allocs/op
BenchmarkMemoryFootprint
BenchmarkMemoryFootprint/size-1000
BenchmarkMemoryFootprint/size-1000-16         	    1851	    600820 ns/op
BenchmarkMemoryFootprint/size-10000
BenchmarkMemoryFootprint/size-10000-16        	     186	   6214802 ns/op
BenchmarkMemoryFootprint/size-100000
BenchmarkMemoryFootprint/size-100000-16       	      15	  72804193 ns/op
BenchmarkHighContention
BenchmarkHighContention-16                    	 3461953	       371.7 ns/op	     280 B/op	       8 allocs/op
BenchmarkLowContention
BenchmarkLowContention-16                     	 4444207	       270.4 ns/op	     378 B/op	      14 allocs/op
BenchmarkPutWithCallback
BenchmarkPutWithCallback-16                   	 1418839	       861.3 ns/op	     380 B/op	      10 allocs/op
BenchmarkPutWithoutCallback
BenchmarkPutWithoutCallback-16                	 1348113	       829.6 ns/op	     384 B/op	      10 allocs/op
BenchmarkRealisticWorkload
BenchmarkRealisticWorkload-16                 	11981487	       102.0 ns/op	     215 B/op	       7 allocs/op
BenchmarkHeapOperations
BenchmarkHeapOperations-16                    	28421942	        43.07 ns/op	      12 B/op	       1 allocs/op
BenchmarkSmallShards
BenchmarkSmallShards-16                       	 1225246	       819.4 ns/op
BenchmarkMediumShards
BenchmarkMediumShards-16                      	 1404387	       825.0 ns/op
BenchmarkLargeShards
BenchmarkLargeShards-16                       	 1463619	       777.2 ns/op
```
---

