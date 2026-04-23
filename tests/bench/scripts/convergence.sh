#!/usr/bin/env bash
# convergence.sh — тест: время конвергенции кластера от 2 до 10 нод.
# Запуск: ./convergence.sh [keys] [poll_interval_s] [timeout_s]
# Пример: ./convergence.sh 100 1 120
#
# Для каждого размера кластера (2..10):
# 1. Поднимаем кластер через docker-compose.
# 2. Пишем KEYS ключей на node1 (порт 8081) через lume-cli.
# 3. Опрашиваем все остальные ноды (node2..nodeN) через lume-cli get.
# 4. Фиксируем время, когда все ноды видят все ключи (100% конвергенция).
# 5. Останавливаем кластер.

now_ms() {
    python3 -c 'import time; print(int(time.time() * 1000))'
}

set -euo pipefail

KEYS="${1:-100}"
POLL_S="${2:-1}"
TIMEOUT_S="${3:-120}"
TIMEOUT_MS=$(( TIMEOUT_S * 1000 ))

LUME_CLI="${LUME_CLI:-lume-cli}"
CLUSTER_DIR="$(dirname "$0")/../../cluster"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROJECT="lume_convergence"

CSV="$RESULTS_DIR/convergence_$(date +%Y%m%d_%H%M%S).csv"
echo "nodes,keys,convergence_ms" > "$CSV"

echo "=== convergence test: keys=$KEYS poll=${POLL_S}s timeout=${TIMEOUT_S}s ==="

trap 'docker compose -f "$COMPOSE" -p "$PROJECT" down -v 2>/dev/null || true' EXIT

for nodes in 2 3 4 5 6 7 8 9 10; do
    profile="${nodes}-node"
    printf "\n--- %d-node cluster ---\n" "$nodes"

    # [1] Поднимаем кластер
    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" up -d --build --wait
    echo "    cluster ready"

    # [2] Пишем ключи на node1 (порт 8081)
    echo "    writing $KEYS keys to node1 (port 8081)..."
    for i in $(seq 0 $(( KEYS - 1 ))); do
        "$LUME_CLI" -s "localhost:8081" set "key_${i}" "value_${i}" > /dev/null 2>&1
    done
    WRITE_DONE=$(now_ms)
    echo "    write done"

    # [3] Опрашиваем node2..nodeN, ждём 100% конвергенцию на всех
    echo "    polling nodes 2..$nodes for convergence..."

    CONVERGED="false"
    while true; do
        ALL_OK="true"

        for n in $(seq 2 "$nodes"); do
            port=$(( 8080 + n ))
            FOUND=0
            for i in $(seq 0 $(( KEYS - 1 ))); do
                result=$("$LUME_CLI" -s "localhost:${port}" get "key_${i}" 2>/dev/null || echo "")
                echo "$result" | grep -q 'Key:' && FOUND=$(( FOUND + 1 ))
            done

            if [ "$FOUND" -lt "$KEYS" ]; then
                ALL_OK="false"
            fi
        done

        NOW=$(now_ms)
        ELAPSED=$(( NOW - WRITE_DONE ))

        if [ "$ALL_OK" = "true" ]; then
            CONVERGED="true"
            SYNC_MS="$ELAPSED"
            echo "    100% convergence in ${SYNC_MS}ms"
            break
        fi

        if [ "$ELAPSED" -gt "$TIMEOUT_MS" ]; then
            SYNC_MS="TIMEOUT"
            echo "    TIMEOUT: convergence not reached in ${TIMEOUT_S}s"
            break
        fi

        # Промежуточный статус — опрашиваем одну ноду для лога
        SAMPLE_PORT=$(( 8080 + nodes ))
        SAMPLE_FOUND=0
        for i in $(seq 0 $(( KEYS - 1 ))); do
            result=$("$LUME_CLI" -s "localhost:${SAMPLE_PORT}" get "key_${i}" 2>/dev/null || echo "")
            echo "$result" | grep -q 'Key:' && SAMPLE_FOUND=$(( SAMPLE_FOUND + 1 ))
        done
        PCT=$(awk -v f="$SAMPLE_FOUND" -v t="$KEYS" 'BEGIN{printf "%.1f", f*100/t}')
        echo "    ${ELAPSED}ms: node${nodes} has ${SAMPLE_FOUND}/${KEYS} (${PCT}%)"

        sleep "$POLL_S"
    done

    echo "$nodes,$KEYS,${SYNC_MS}" >> "$CSV"

    # [4] Останавливаем кластер
    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" down -v 2>/dev/null
    echo "    cluster stopped"
done

echo ""
echo "=== Results ==="
column -t -s',' "$CSV"
echo ""
echo "Saved: $CSV"