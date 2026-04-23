#!/usr/bin/env bash
# convergence.sh — тест: время конвергенции кластера от 2 до 10 нод.
# Запуск: ./convergence.sh [keys] [repeats] [poll_interval_s] [timeout_s]
# Пример: ./convergence.sh 100 5 1 120
#
# Для каждого размера кластера (2..10) повторяет замер REPEATS раз:
# 1. Поднимаем кластер через docker-compose.
# 2. Пишем KEYS ключей на node1 (порт 8081) через lume-cli.
# 3. Опрашиваем все остальные ноды (node2..nodeN) через lume-cli get.
# 4. Фиксируем время, когда все ноды видят все ключи (100% конвергенция).
# 5. Повторяем шаги 2-4 ещё REPEATS-1 раз (кластер остаётся поднятым).
# 6. Останавливаем кластер.

now_ms() {
    python3 -c 'import time; print(int(time.time() * 1000))'
}

set -euo pipefail

KEYS="${1:-100}"
REPEATS="${2:-5}"
POLL_S="${3:-1}"
TIMEOUT_S="${4:-120}"
TIMEOUT_MS=$(( TIMEOUT_S * 1000 ))

LUME_CLI="${LUME_CLI:-lume-cli}"
CLUSTER_DIR="$(dirname "$0")/../../cluster"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROJECT="lume_convergence"

CSV="$RESULTS_DIR/convergence_$(date +%Y%m%d_%H%M%S).csv"
echo "nodes,keys,run,convergence_ms" > "$CSV"

echo "=== convergence test: keys=$KEYS repeats=$REPEATS poll=${POLL_S}s timeout=${TIMEOUT_S}s ==="

trap 'docker compose -f "$COMPOSE" -p "$PROJECT" down -v 2>/dev/null || true' EXIT

for nodes in 2 3 4 5 6 7 8 9 10; do
    profile="${nodes}-node"
    printf "\n--- %d-node cluster ---\n" "$nodes"

    # Поднимаем кластер один раз для всех повторов
    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" up -d --build --wait
    echo "    cluster ready"

    for run in $(seq 1 "$REPEATS"); do
        # Уникальный префикс ключей для каждого прогона,
        # чтобы предыдущие ключи не влияли на результат.
        KEY_PREFIX="r${run}_key_"

        printf "    [run %d/%d] writing %d keys... " "$run" "$REPEATS" "$KEYS"
        for i in $(seq 0 $(( KEYS - 1 ))); do
            "$LUME_CLI" -s "localhost:8081" set "${KEY_PREFIX}${i}" "value_${i}" > /dev/null 2>&1
        done
        WRITE_DONE=$(now_ms)
        echo "done. polling..."

        # Опрашиваем node2..nodeN, ждём 100% конвергенцию на всех
        while true; do
            ALL_OK="true"

            for n in $(seq 2 "$nodes"); do
                port=$(( 8080 + n ))
                FOUND=0
                for i in $(seq 0 $(( KEYS - 1 ))); do
                    result=$("$LUME_CLI" -s "localhost:${port}" get "${KEY_PREFIX}${i}" 2>/dev/null || echo "")
                    echo "$result" | grep -q 'Key:' && FOUND=$(( FOUND + 1 ))
                done

                if [ "$FOUND" -lt "$KEYS" ]; then
                    ALL_OK="false"
                fi
            done

            NOW=$(now_ms)
            ELAPSED=$(( NOW - WRITE_DONE ))

            if [ "$ALL_OK" = "true" ]; then
                SYNC_MS="$ELAPSED"
                printf "    [run %d/%d] 100%% convergence in %sms\n" "$run" "$REPEATS" "$SYNC_MS"
                break
            fi

            if [ "$ELAPSED" -gt "$TIMEOUT_MS" ]; then
                SYNC_MS="TIMEOUT"
                printf "    [run %d/%d] TIMEOUT: convergence not reached in %ss\n" "$run" "$REPEATS" "$TIMEOUT_S"
                break
            fi

            # Промежуточный статус — последняя нода
            SAMPLE_PORT=$(( 8080 + nodes ))
            SAMPLE_FOUND=0
            for i in $(seq 0 $(( KEYS - 1 ))); do
                result=$("$LUME_CLI" -s "localhost:${SAMPLE_PORT}" get "${KEY_PREFIX}${i}" 2>/dev/null || echo "")
                echo "$result" | grep -q 'Key:' && SAMPLE_FOUND=$(( SAMPLE_FOUND + 1 ))
            done
            PCT=$(awk -v f="$SAMPLE_FOUND" -v t="$KEYS" 'BEGIN{printf "%.1f", f*100/t}')
            printf "              %sms: node%d has %d/%d (%s%%)\n" "$ELAPSED" "$nodes" "$SAMPLE_FOUND" "$KEYS" "$PCT"

            sleep "$POLL_S"
        done

        echo "$nodes,$KEYS,$run,${SYNC_MS}" >> "$CSV"
    done

    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" down -v 2>/dev/null
    echo "    cluster stopped"
done

echo ""
echo "=== Results ==="
column -t -s',' "$CSV"
echo ""
echo "Saved: $CSV"