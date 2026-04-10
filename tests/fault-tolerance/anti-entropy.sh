#!/usr/bin/env bash
# anti-entropy.sh — тест 2.3: стоп узла → накопить обновления → старт → ждать sync.
# Запуск: ./anti-entropy.sh [keys] [conn] [rpc] [poll_interval_s] [sample_size]
# Пример: ./anti-entropy.sh 10000 1 50 2 400
#
# Топология: 3 узла (docker-compose.yaml --profile 3-node, WAL off).
# 1. Стартуем кластер.
# 2. Останавливаем node3.
# 3. Пишем M ключей через lume-bench на node1 (порт 8081).
# 4. Запускаем node3.
# 5. Поллим lume-cli на node3 (порт 8083) — считаем видимые ключи (по выборке).
# 6. Фиксируем время до 100% конвергенции.

now_ms() {
    python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
}

set -euo pipefail

KEYS="${1:-5000}"
CONN="${2:-1}"
RPC="${3:-50}"
POLL_S="${4:-2}"
# Размер выборки для проверки — опрашиваем не все KEYS, а SAMPLE ключей.
# Для небольших KEYS (<= SAMPLE) проверяем все; для больших — равномерную выборку.
SAMPLE="${5:-200}"
CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_CLI="${LUME_CLI:-lume-cli}"
LUME_BENCH="${LUME_BENCH:-lume-bench}"
RESULTS_DIR="$(dirname "$0")/../bench/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="3-node"

echo "=== anti-entropy test: 3-node cluster, $KEYS keys, sample=$SAMPLE ==="

echo "[1] Starting 3-node cluster..."
docker compose -f "$COMPOSE" --profile "$PROFILE" up -d --build --wait
echo "    cluster ready"

echo "[2] Stopping node3..."
docker stop lume-node3
echo "    node3 stopped"

echo "[3] Writing $KEYS keys on node1 (port 8081)..."
"$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" \
    -d 60 -w 0 -t set --pool-size "$KEYS" \
    > /dev/null 2>&1 || true
echo "    write done"

echo "[4] Starting node3..."
docker start lume-node3

# Ждём, пока node3 станет healthy
for i in $(seq 1 30); do
    status=$(docker inspect --format='{{.State.Health.Status}}' lume-node3 2>/dev/null || echo "unknown")
    [ "$status" = "healthy" ] && break
    sleep 1
done
SYNC_START=$(now_ms)
echo "    node3 healthy, measuring sync..."

# Строим список индексов для проверки: равномерная выборка из [0, KEYS-1]
# При KEYS <= SAMPLE — проверяем все; иначе — SAMPLE равномерно распределённых.
if [ "$KEYS" -le "$SAMPLE" ]; then
    CHECK_COUNT="$KEYS"
    STEP=1
else
    CHECK_COUNT="$SAMPLE"
    STEP=$(( KEYS / SAMPLE ))
fi

echo "[5] Polling node3 (port 8083), checking $CHECK_COUNT keys (step=$STEP)..."

while true; do
    FOUND=0
    idx=0
    checked=0
    while [ "$checked" -lt "$CHECK_COUNT" ]; do
        key="key_${idx}"
        result=$("$LUME_CLI" -s localhost:8083 get "$key" 2>/dev/null || echo "")
        echo "$result" | grep -q 'Key:' && FOUND=$(( FOUND + 1 ))
        idx=$(( idx + STEP ))
        checked=$(( checked + 1 ))
    done

    NOW=$(now_ms)
    ELAPSED=$(( NOW - SYNC_START ))
    # Экстраполируем на весь пул
    ESTIMATED=$(awk -v f="$FOUND" -v s="$CHECK_COUNT" -v t="$KEYS" \
        'BEGIN{printf "%.0f", f * t / s}')
    PCT=$(awk -v f="$FOUND" -v s="$CHECK_COUNT" 'BEGIN{printf "%.1f", f*100/s}')
    echo "    ${ELAPSED}ms: sample ${FOUND}/${CHECK_COUNT} (${PCT}%) → est. ${ESTIMATED}/${KEYS}"

    if [ "$FOUND" -eq "$CHECK_COUNT" ]; then
        SYNC_MS="$ELAPSED"
        echo "    100% sample convergence reached in ${SYNC_MS}ms"
        break
    fi

    # Таймаут: если прошло больше 300 секунд — прерываем
    if [ "$ELAPSED" -gt 300000 ]; then
        SYNC_MS="TIMEOUT(>${ELAPSED}ms)"
        echo "    TIMEOUT: sync did not complete in 300s"
        break
    fi

    sleep "$POLL_S"
done

echo ""
echo "=== Summary ==="
echo "  keys_written       = $KEYS"
echo "  sample_size        = $CHECK_COUNT"
echo "  sync_time_ms       = ${SYNC_MS:-N/A}"

CSV="$RESULTS_DIR/anti_entropy_$(date +%Y%m%d_%H%M%S).csv"
echo "keys_written,sample_size,sync_time_ms" > "$CSV"
echo "$KEYS,$CHECK_COUNT,${SYNC_MS:-0}" >> "$CSV"
echo "Saved: $CSV"

echo ""
echo "[6] Tearing down..."
docker compose -f "$COMPOSE" --profile "$PROFILE" down -v