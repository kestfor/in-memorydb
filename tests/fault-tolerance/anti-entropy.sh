#!/usr/bin/env bash
# anti-entropy.sh — тест 2.3: стоп узла → накопить обновления → старт → ждать sync.
# Запуск: ./anti-entropy.sh [keys] [conn] [rpc] [poll_interval_s]
# Пример: ./anti-entropy.sh 5000 10 4 2
#
# Топология: 3 узла (docker-compose-3.yaml, WAL off).
# 1. Стартуем кластер.
# 2. Останавливаем node3.
# 3. Пишем M ключей через lume-bench на node1 (порт 8081).
# 4. Запускаем node3.
# 5. Поллим lume-cli на node3 (порт 8083) — считаем видимые ключи.
# 6. Фиксируем время до 100% конвергенции.

set -euo pipefail

KEYS="${1:-5000}"
CONN="${2:-10}"
RPC="${3:-4}"
POLL_S="${4:-2}"
CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_CLI="${LUME_CLI:-lume-cli}"
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../bench/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="3-node"

echo "=== anti-entropy test: 3-node cluster, $KEYS keys ==="

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
SYNC_START=$(date +%s%3N)
echo "    node3 healthy, measuring sync..."

echo "[5] Polling node3 (port 8083) for key visibility..."
PREV_FOUND=-1

while true; do
    FOUND=0
    for i in $(seq 0 $(( KEYS - 1 ))); do
        key="key_${i}"
        result=$("$LUME_CLI" -s localhost:8083 get "$key" 2>/dev/null || echo "")
        echo "$result" | grep -q 'Key:' && FOUND=$(( FOUND + 1 ))
    done

    NOW=$(date +%s%3N)
    ELAPSED=$(( NOW - SYNC_START ))
    PCT=$(awk -v f="$FOUND" -v t="$KEYS" 'BEGIN{printf "%.1f", f*100/t}')
    echo "    ${ELAPSED}ms: found=${FOUND}/${KEYS} (${PCT}%)"

    if [ "$FOUND" -eq "$KEYS" ]; then
        SYNC_MS="$ELAPSED"
        echo "    100% convergence reached in ${SYNC_MS}ms"
        break
    fi

    if [ "$FOUND" -eq "$PREV_FOUND" ]; then
        # Нет прогресса — проверяем ещё раз через poll_interval
        :
    fi
    PREV_FOUND="$FOUND"
    sleep "$POLL_S"
done

echo ""
echo "=== Summary ==="
echo "  keys_written       = $KEYS"
echo "  sync_time_ms       = ${SYNC_MS:-N/A}"

CSV="$RESULTS_DIR/anti_entropy_$(date +%Y%m%d_%H%M%S).csv"
echo "keys_written,sync_time_ms" > "$CSV"
echo "$KEYS,${SYNC_MS:-0}" >> "$CSV"
echo "Saved: $CSV"

echo ""
echo "[6] Tearing down..."
docker compose -f "$COMPOSE" --profile "$PROFILE" down -v