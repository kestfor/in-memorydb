#!/usr/bin/env bash
# wal-recovery.sh — тест 2.2: docker kill + restart, измеряем время WAL-восстановления.
# Запуск: ./wal-recovery.sh [keys] [conn] [rpc]
# Пример: ./wal-recovery.sh 10000 10 4
#
# Топология: 1 узел с WAL-async (docker-compose-1.yaml + wal-async.yaml override).
# Записываем N ключей → docker kill → time docker start → health check → проверяем ключи.

set -euo pipefail

KEYS="${1:-10000}"
CONN="${2:-10}"
RPC="${3:-4}"
CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_CLI="${LUME_CLI:-lume-cli}"
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../bench/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="1-node"
WAL_ASYNC_CFG="$(realpath "$CLUSTER_DIR/configs/wal/wal-async.yaml")"

TMPDIR_REC=$(mktemp -d)
trap 'rm -rf "$TMPDIR_REC"' EXIT

# override: монтируем WAL-конфиг и volume для данных
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

echo "=== WAL recovery test: $KEYS keys, wal-async ==="

echo "[1] Starting node with WAL-async..."
docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p lume_wal_rec up -d --build --wait
echo "    node ready"

echo "[2] Writing $KEYS keys via lume-bench..."
"$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" \
    -d 60 -w 0 -t set --pool-size "$KEYS" \
    -n "wal_recovery_write" -o "$TMPDIR_REC" > /dev/null 2>&1 || true
echo "    write phase done"

echo "[3] docker kill lume-node1..."
docker kill lume-node1
echo "    killed"

echo "[4] Measuring restart + WAL replay time..."
START=$(date +%s%3N)
docker start lume-node1

# Ждём health check
for i in $(seq 1 60); do
    status=$(docker inspect --format='{{.State.Health.Status}}' lume-node1 2>/dev/null || echo "unknown")
    if [ "$status" = "healthy" ]; then
        END=$(date +%s%3N)
        RECOVERY_MS=$(( END - START ))
        echo "    node healthy after ${RECOVERY_MS}ms"
        break
    fi
    sleep 1
done

echo "[5] Checking sample keys via lume-cli..."
FOUND=0
MISSING=0
SAMPLE=100
for i in $(seq 0 $(( SAMPLE - 1 ))); do
    key="key_${i}"
    result=$("$LUME_CLI" -s localhost:8081 get "$key" 2>/dev/null || echo "")
    if echo "$result" | grep -q 'Key:'; then
        FOUND=$(( FOUND + 1 ))
    else
        MISSING=$(( MISSING + 1 ))
    fi
done
echo "    sample $SAMPLE keys: found=$FOUND missing=$MISSING"

echo ""
echo "=== Summary ==="
echo "  total_keys_written = $KEYS"
echo "  recovery_time_ms   = ${RECOVERY_MS:-N/A}"
echo "  sample_found       = $FOUND / $SAMPLE"

CSV="$RESULTS_DIR/wal_recovery_$(date +%Y%m%d_%H%M%S).csv"
echo "keys_written,recovery_ms,sample_found,sample_total" > "$CSV"
echo "$KEYS,${RECOVERY_MS:-0},$FOUND,$SAMPLE" >> "$CSV"
echo "Saved: $CSV"

echo ""
echo "[6] Tearing down..."
docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p lume_wal_rec down -v