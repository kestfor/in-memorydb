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
LUME_BENCH="${LUME_BENCH:-lume-bench}"
CLUSTER_DIR="$(dirname "$0")/../../cluster"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'
PROFILE="1-node"
PROJECT="lume_wal_test"

CSV="$RESULTS_DIR/wal_$(date +%Y%m%d_%H%M%S).csv"
echo "wal_mode,qps,p50,p90,p99" > "$CSV"
echo "=== WAL sweep: c=$CONN r=$RPC duration=${DURATION}s ==="

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"

TMPDIR_WAL=$(mktemp -d)
trap 'rm -rf "$TMPDIR_WAL"; docker compose -f "$COMPOSE" --profile "$PROFILE" -p "$PROJECT" down -v 2>/dev/null || true' EXIT

run_mode() {
    local mode="$1"
    shift
    local extra_flags=("$@")  # дополнительные -f флаги для override

    printf "\n--- mode=%s ---\n" "$mode"

    docker compose -f "$COMPOSE" "${extra_flags[@]}" --profile "$PROFILE" \
        -p "$PROJECT" up -d --build --wait 2>/dev/null

    sleep 2

    local out
    out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t set 2>/dev/null)

    local qps p50 p90 p99
    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')

    echo "$mode,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "  [%s] qps=%-8s p50=%s p90=%s p99=%s\n" "$mode" "$qps" "$p50" "$p90" "$p99"

    docker compose -f "$COMPOSE" "${extra_flags[@]}" --profile "$PROFILE" \
        -p "$PROJECT" down -v 2>/dev/null
}

# wal-off: base.yaml (persistence.enabled=false)
run_mode "wal-off"

# wal-async и wal-sync: override монтирует WAL-конфиг и отдельный volume
for wmode in wal-async wal-sync; do
    cfg_path="$(realpath "$CLUSTER_DIR/configs/wal/${wmode}.yaml")"
    override="$TMPDIR_WAL/override_${wmode}.yaml"
    cat > "$override" <<EOF
services:
  node1:
    volumes:
      - ${cfg_path}:/lume-config/config.yaml
      - lume_wal_data_${wmode}:/wal_data
volumes:
  lume_wal_data_${wmode}:
EOF
    run_mode "$wmode" -f "$override"
done

echo ""
echo "Saved: $CSV"
