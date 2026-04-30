#!/usr/bin/env bash
# wal-recovery.sh — тест 4.7.2: время восстановления данных из WAL.
#
# Один узел, WAL-async. Записываем N ключей, kill процесс, перезапускаем,
# и берём время восстановления НЕ по docker health (имеет накладные расходы
# на старт контейнера / gRPC / membership), а из лога самой ноды:
#
#   storage.restoreFromWAL: data restored successfully  elapsed (sec): X.YYY
#
# REPEATS прогонов на каждый размер; в итоговую таблицу — медиана.
# Soft-limit: ~15 минут.
#
# Запуск:
#   ./wal-recovery.sh [conn] [workers] [value_size] [repeats]
#   ./wal-recovery.sh 4 8 16 5

set -euo pipefail

CONN="${1:-4}"
WORKERS="${2:-8}"
VALUE_SIZE="${3:-16}"
REPEATS="${4:-5}"

# Размеры пула ключей. 6 точек в логарифмической шкале от 10k до 3M.
KEY_SIZES=(10000 30000 100000 300000 1000000 3000000)

CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_LOAD="${LUME_LOAD:-lume-load}"
RESULTS_DIR="$(dirname "$0")/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="1-node"
WAL_ASYNC_CFG="$(realpath "$CLUSTER_DIR/configs/wal/wal-async.yaml")"

TMPDIR_REC=$(mktemp -d)
trap 'rm -rf "$TMPDIR_REC"; teardown' EXIT

OVERRIDE="$TMPDIR_REC/override.yaml"
cat > "$OVERRIDE" <<EOF
services:
  node1:
    volumes:
      - ${WAL_ASYNC_CFG}:/lume-config/config.yaml
      - lume_wal_rec:/wal_data
volumes:
  lume_wal_rec:
EOF

PROJECT="lume_wal_rec"

teardown() {
    docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p "$PROJECT" down -v >/dev/null 2>&1 || true
}

start_node() {
    docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p "$PROJECT" up -d --build --wait >/dev/null
}

# Извлекает elapsed (sec) из ПОСЛЕДНЕГО сообщения "data restored successfully"
# в логах контейнера, СТРОГО позже отметки $1 (RFC3339). --since нужен,
# потому что docker logs не очищается между kill+start и содержит лог
# первого старта (пустой WAL → elapsed=0).
#
# slog в проде пишет однострочный JSON, поэтому grep по строке + jq
# достаточно. Берём ПОСЛЕДНЮЮ матчащую строку — на случай нескольких
# повторных рестартов внутри окна --since.
extract_elapsed() {
    local since="$1"
    docker logs --since "$since" lume-node1 2>&1 \
        | grep -F '"msg":"storage.restoreFromWAL: data restored successfully"' \
        | tail -n 1 \
        | jq -r '."elapsed (sec)" // empty' 2>/dev/null
}

median() {
    printf '%s\n' "$@" | sort -n | awk '
        { a[NR]=$1 }
        END {
            if (NR==0) { print "0"; exit }
            if (NR%2==1) print a[(NR+1)/2]
            else printf "%.4f\n", (a[NR/2]+a[NR/2+1])/2
        }'
}

CSV="$RESULTS_DIR/wal_recovery_$(date +%Y%m%d_%H%M%S).csv"
echo "keys,repeat,write_sec,recovery_sec_log" > "$CSV"

SUMMARY_CSV="$RESULTS_DIR/wal_recovery_summary_$(date +%Y%m%d_%H%M%S).csv"
echo "keys,repeats,recovery_sec_median" > "$SUMMARY_CSV"

echo "=== wal-recovery: sizes=[${KEY_SIZES[*]}], repeats=${REPEATS}, conn=${CONN}, workers=${WORKERS}, value_size=${VALUE_SIZE} ==="

for KEYS in "${KEY_SIZES[@]}"; do
    REC_VALS=()

    for R in $(seq 1 "$REPEATS"); do
        echo ""
        echo "[keys=${KEYS}, repeat=${R}/${REPEATS}] starting node..."
        teardown
        start_node

        echo "    inserting ${KEYS} keys via lume-load (conn=${CONN}, workers=${WORKERS})..."
        WRITE_START=$(date +%s)
        "$LUME_LOAD" -s localhost:8081 \
            -n "$KEYS" -t register -c "$CONN" -w "$WORKERS" \
            --value-size "$VALUE_SIZE" --quiet \
            > /dev/null 2>&1 || {
                echo "    WARN: lume-load returned non-zero exit code"
            }
        WRITE_SEC=$(( $(date +%s) - WRITE_START ))
        echo "    write done in ${WRITE_SEC}s"

        # Дать WAL-flush'у успеть слить буфер на диск (async, 100ms interval).
        sleep 1

        echo "    docker kill..."
        docker kill lume-node1 >/dev/null

        # Маркер времени для --since: всё, что было в логах ДО рестарта (включая
        # лог первого старта с пустым WAL), отсекается. Берём UTC RFC3339 за
        # секунду до старта, чтобы не потерять запись из-за округления.
        SINCE=$(date -u -d '-1 second' +%Y-%m-%dT%H:%M:%S 2>/dev/null \
                || date -u -v-1S +%Y-%m-%dT%H:%M:%S)

        echo "    docker start (lazy: ждём появления записи в логе с --since=${SINCE})..."
        docker start lume-node1 >/dev/null

        # Поллим лог до появления "data restored successfully" ПОСЛЕ рестарта.
        ELAPSED=""
        for i in $(seq 1 120); do
            ELAPSED=$(extract_elapsed "$SINCE")
            [ -n "$ELAPSED" ] && break
            sleep 1
        done

        if [ -z "$ELAPSED" ]; then
            echo "    WARN: лог восстановления не найден за 120s, пропускаю"
            ELAPSED="NaN"
        else
            echo "    recovery_sec=${ELAPSED}"
            REC_VALS+=("$ELAPSED")
        fi

        echo "${KEYS},${R},${WRITE_SEC},${ELAPSED}" >> "$CSV"
    done

    if [ "${#REC_VALS[@]}" -gt 0 ]; then
        REC_MED=$(median "${REC_VALS[@]}")
    else
        REC_MED="NaN"
    fi
    echo ""
    echo ">>> keys=${KEYS}: recovery_median=${REC_MED}s (n=${#REC_VALS[@]})"
    echo "${KEYS},${#REC_VALS[@]},${REC_MED}" >> "$SUMMARY_CSV"
done

echo ""
echo "=== Summary ==="
column -t -s, "$SUMMARY_CSV" 2>/dev/null || cat "$SUMMARY_CSV"
echo ""
echo "Raw:     $CSV"
echo "Summary: $SUMMARY_CSV"
