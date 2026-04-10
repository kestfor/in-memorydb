#!/usr/bin/env bash
# wal.sh — WAL overhead: запускает Lume поочерёдно с wal-off / wal-async / wal-sync,
#           прогоняет lume-bench с фиксированными c* и r*.
# Запуск: ./wal.sh <conn> <rpc> [duration]
# Пример: ./wal.sh 10 4 30
#
# Предполагает, что docker-compose.yaml + WAL-конфиги доступны в tests/cluster/.
# Каждая итерация: docker compose up → bench → docker compose down.

set -euo pipefail

CONN="${1:?Usage: $0 <conn> <rpc> [duration]}"
RPC="${2:?}"
DURATION="${3:-30}"
WARMUP=5
PORT=8081
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
CLUSTER_DIR="$(dirname "$0")/../../cluster"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

CSV="$RESULTS_DIR/wal_$(date +%Y%m%d_%H%M%S).csv"
echo "wal_mode,qps,p50,p90,p99" > "$CSV"
echo "=== WAL sweep: c=$CONN r=$RPC duration=${DURATION}s ==="

run_with_config() {
    local mode="$1"   # wal-off | wal-async | wal-sync
    local compose_file="$2"
    local config_volume="$3"

    printf "\n--- mode=%s ---\n" "$mode"

    # Стартуем кластер с нужным конфигом
    docker compose -f "$compose_file" \
        --env-file /dev/null \
        -p "lume_wal_test" \
        up -d --build \
        --wait 2>/dev/null

    sleep 2  # дать узлу полностью стартовать

    out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t set 2>/dev/null)

    docker compose -f "$compose_file" -p "lume_wal_test" down -v 2>/dev/null

    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')

    echo "$mode,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "  qps=%-8s p50=%s p90=%s p99=%s\n" "$qps" "$p50" "$p90" "$p99"
}

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="1-node"

TMPDIR_WAL=$(mktemp -d)
trap 'rm -rf "$TMPDIR_WAL"' EXIT

# wal-off: base.yaml (persistence.enabled=false)
run_with_config "wal-off" "$COMPOSE" ""

# wal-async / wal-sync: override монтирует WAL-конфиг и volume для данных
for mode in wal-async wal-sync; do
    cfg_path="$(realpath "$CLUSTER_DIR/configs/wal/${mode}.yaml")"
    override="$TMPDIR_WAL/override_${mode}.yaml"
    cat > "$override" <<EOF
services:
  node1:
    volumes:
      - ${cfg_path}:/lume-config/config.yaml
      - lume_wal_data:/wal_data
volumes:
  lume_wal_data:
EOF

    docker compose -f "$COMPOSE" -f "$override" --profile "$PROFILE" \
        -p "lume_wal_test" up -d --build --wait 2>/dev/null

    sleep 2

    out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t set 2>/dev/null)

    docker compose -f "$COMPOSE" -f "$override" --profile "$PROFILE" \
        -p "lume_wal_test" down -v 2>/dev/null

    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')

    echo "$mode,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "  [%s] qps=%-8s p50=%s p90=%s p99=%s\n" "$mode" "$qps" "$p50" "$p90" "$p99"
done

echo ""
echo "Saved: $CSV"