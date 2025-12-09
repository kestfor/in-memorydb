# lume-bench - Redis-bench style benchmark tool

Инструмент нагрузочного тестирования для LUME in-memory database, аналог redis-bench.

## Быстрый старт

### Компиляция
```bash
go build ./cmd/lume-bench
```

### Базовое использование
```bash
# Простой тест (100k запросов, 50 клиентов)
./lume-bench -c 50 -n 100000

# Тест SET операций (60 секунд)
./lume-bench -t set -c 100 -d 60s

# Все тесты подряд
./lume-bench -t all -c 50 -d 30s

# С конфигурационным файлом
./lume-bench --config benchmark.yaml
```

## Параметры

### Конфигурация
- `--config <file>` - Путь к YAML конфигурационному файлу

### Подключение
- `--host <hostname>` - Хост сервера (default: 127.0.0.1)
- `-p, --port <port>` - Порт сервера (default: 50051)

### Тестирование
- `-c, --clients <num>` - Количество параллельных клиентов (default: 50)
- `-n, --requests <num>` - Общее количество запросов (0 = использовать duration)
- `-d, --duration <time>` - Длительность теста (например: 60s, 1m, 30s) (default: 30s)
- `-t, --test <types>` - Типы тестов: **set**, **get**, **apply**, **delete**, **mixed**, **all** (default: all)
- `--rps <rate>` - Ограничение RPS (0 = без ограничений)

### Данные
- `--size <bytes>` - Размер payload в байтах (default: 256)
- `--step <value>` - Шаг инкремента/декремента счетчика (default: 1)

### Mixed workload
- `--set-pct <pct>` - Процент SET операций (default: 20)
- `--get-pct <pct>` - Процент GET операций (default: 40)
- `--apply-pct <pct>` - Процент APPLY операций (default: 30)
- `--delete-pct <pct>` - Процент DELETE операций (default: 10)

### Вывод
- `-o, --output <dir>` - Директория для результатов (default: .)
- `--csv <file>` - Экспорт результатов в CSV файл
- `--json <file>` - Экспорт результатов в JSON файл
- `-q, --quiet` - Тихий режим (только итоговые результаты)
- `--progress` - Показывать реалтайм прогресс (default: true)

## Конфигурационный файл

Пример `benchmark.yaml`:

```yaml
# LUME Benchmark Configuration
host: "127.0.0.1"
port: 50051

# Test duration per stage
duration: 30s

# Number of concurrent clients
concurrency: 50

# Rate limit (requests per second), 0 = unlimited
rate_limit_rps: 0

# Payload size in bytes
payload_size: 256

# Counter increment step for APPLY tests
counter_step: 1

# Tests to run: set, apply, get, mixed, all
tests:
  - set
  - apply
  - get
  - mixed

# Mixed workload percentages (must sum to 100)
mixed_set_pct: 20
mixed_get_pct: 40
mixed_apply_pct: 30
mixed_delete_pct: 10

# Output settings
output_dir: "./results"
json_output: "results.json"
csv_output: "results.csv"
quiet: false
show_progress: true
```

Флаги командной строки имеют приоритет над значениями из конфигурационного файла.

## Примеры

### 1. Максимальный throughput
```bash
./lume-bench -t set -c 100 -d 60s --rps 0
```

### 2. Латентность при фиксированной нагрузке
```bash
./lume-bench -t get -c 10 -d 60s --rps 1000
```

### 3. Реалистичная смешанная нагрузка
```bash
./lume-bench -t mixed -c 50 -d 120s --rps 10000 \
  --set-pct 20 --get-pct 50 --apply-pct 25 --delete-pct 5
```

### 4. Быстрый тест с экспортом
```bash
./lume-bench -t all -c 50 -n 50000 --csv results.csv --json results.json
```

### 5. Тихий режим для CI/CD
```bash
./lume-bench -t set -c 50 -n 100000 -q
```

### 6. Несколько типов тестов
```bash
./lume-bench -t set,get,mixed -c 50 -d 30s
```

### 7. С конфигом и переопределением
```bash
./lume-bench --config benchmark.yaml -d 2m -c 100
```

## Пример вывода

```
=================================================
         LUME-BENCH v2.0
         Redis-bench style benchmark tool
=================================================
Target: 127.0.0.1:50051
Duration: 60s per test
Parallel clients: 50
Rate limit: unlimited
Payload: 256 bytes
Tests: set, get, apply, mixed
=================================================

Preloading 50000 keys...
Preloading: 50000/50000 (100.0%) - Done!

[60.0s] 1204560 requests | 20076 req/s | success: 1204560 | failed: 0

====== SET ======
  1204560 requests completed in 60.02 seconds
  50 parallel clients
  256 bytes payload

Throughput: 20076.45 requests/second

Latency distribution:
  50%     2.48 ms
  90%     3.15 ms
  95%     3.85 ms
  99%     5.21 ms
  99.9%   8.42 ms

=================================================
         ALL TESTS COMPLETED!
=================================================
```

## Типы тестов

| Тест | Описание |
|------|----------|
| `set` | Тестирование операции SET (запись регистров LWW) |
| `get` | Тестирование операции GET (чтение ключей) |
| `apply` | Тестирование операции APPLY (инкремент/декремент PN-счетчиков) |
| `mixed` | Смешанная нагрузка с настраиваемыми процентами операций |
| `delete` | DELETE тестируется как часть mixed workload |
| `all` | Последовательно запускает: set, get, apply, mixed |

