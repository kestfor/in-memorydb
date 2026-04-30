#!/usr/bin/env bash
# wal-cost.sh — тест 4.7.3: стоимость журнала для производительности.
#
# Один узел. Сравниваем режимы WAL:
#   - none       (persistence.enabled = false)
#   - sync       (sync_mode=true, flush_interval=100ms)
#   - async-100ms, async-500ms, async-1s, async-5s
# Нагрузки: write-only (-t set) и mixed.
#
# REPEATS прогонов на каждую комбинацию; в итоговую таблицу — медиана QPS и p99.
# Soft-limit: ~15 минут.
#
# Запуск:
#   ./wal-cost.sh [conn] [rpc] [duration_per_run] [repeats]
#   ./wal-cost.sh 1 100 30 5

set -euo pipefail

CONN="${1:-1}"
RPC="${2:-100}"
DUR="${3:-30}"
REPEATS="${4:-5}"
WARMUP=3

MODES=(none sync async-100ms async-500ms async-1s async-5s)
WORKLOADS=(set mixed)

CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_BENCH="${LUME_BENCH:-lume-bench}"
RESULTS_DIR="$(dirname "$0")/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="1-node"
BASE_CFG="$(realpath "$CLUSTER_DIR/configs/base.yaml")"
STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

TMPDIR_WC=$(mktemp -d)
PROJECT="lume_wal_cost"

teardown() {
    docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p "$PROJECT" down -v >/dev/null 2>&1 || true
}

trap 'teardown; rm -rf "$TMPDIR_WC"' EXIT

# Генерирует config.yaml для конкретного режима в $TMPDIR_WC/cfg-<mode>.yaml
make_cfg() {
    local mode="$1"
    local cfg="$TMPDIR_WC/cfg-${mode}.yaml"
    case "$mode" in
        none)
            cp "$BASE_CFG" "$cfg"
            ;;
        sync)
            cat > "$cfg" <<EOF
gossip:
  bind_address: "0.0.0.0"
  port: 8081
  fanout: 3
  interval: 10s
  retries: 2
engine:
  shards_num: 256
buffer:
  size: 1000
persistence:
  enabled: true
  wal:
    path: /wal_data
    flush_interval: 100ms
    sync_mode: true
    batch_size: 1000
    segment_threshold: 10000
security:
  mode: disabled
trace:
  enabled: false
EOF
            ;;
        async-*)
            local interval="${mode#async-}"
            cat > "$cfg" <<EOF
gossip:
  bind_address: "0.0.0.0"
  port: 8081
  fanout: 3
  interval: 10s
  retries: 2
engine:
  shards_num: 256
buffer:
  size: 1000
persistence:
  enabled: true
  wal:
    path: /wal_data
    flush_interval: ${interval}
    sync_mode: false
    batch_size: 1000
    segment_threshold: 10000
security:
  mode: disabled
trace:
  enabled: false
EOF
            ;;
    esac
    echo "$cfg"
}

OVERRIDE="$TMPDIR_WC/override.yaml"
write_override() {
    local cfg="$1"
    local need_volume="$2"  # "yes" если режим использует WAL
    if [ "$need_volume" = "yes" ]; then
        cat > "$OVERRIDE" <<EOF
services:
  node1:
    volumes:
      - ${cfg}:/lume-config/config.yaml
      - lume_wal_cost:/wal_data
volumes:
  lume_wal_cost:
EOF
    else
        cat > "$OVERRIDE" <<EOF
services:
  node1:
    volumes:
      - ${cfg}:/lume-config/config.yaml
EOF
    fi
}

start_node() {
    docker compose -f "$COMPOSE" -f "$OVERRIDE" --profile "$PROFILE" -p "$PROJECT" up -d --build --wait >/dev/null
}

run_bench() {
    local wl="$1"
    local out qps p99
    out=$("$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" -d "$DUR" -w "$WARMUP" -t "$wl" 2>/dev/null || true)
    qps=$(echo "$out" | grep -m1 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p99=$(echo "$out" | grep -m1 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    echo "${qps:-0} ${p99:-0}"
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

CSV="$RESULTS_DIR/wal_cost_$(date +%Y%m%d_%H%M%S).csv"
echo "mode,workload,repeat,qps,p99_ms" > "$CSV"

SUMMARY_CSV="$RESULTS_DIR/wal_cost_summary_$(date +%Y%m%d_%H%M%S).csv"
echo "mode,workload,qps_median,p99_median_ms" > "$SUMMARY_CSV"

echo "=== wal-cost: modes=[${MODES[*]}], workloads=[${WORKLOADS[*]}], repeats=${REPEATS}, dur=${DUR}s ==="

for MODE in "${MODES[@]}"; do
    CFG=$(make_cfg "$MODE")
    if [ "$MODE" = "none" ]; then
        write_override "$CFG" "no"
    else
        write_override "$CFG" "yes"
    fi

    for WL in "${WORKLOADS[@]}"; do
        QPS_VALS=()
        P99_VALS=()

        for R in $(seq 1 "$REPEATS"); do
            echo ""
            echo "[mode=${MODE}, workload=${WL}, repeat=${R}/${REPEATS}] starting node..."
            teardown
            start_node

            read -r QPS P99 < <(run_bench "$WL")
            echo "    qps=${QPS}  p99=${P99}"

            echo "${MODE},${WL},${R},${QPS},${P99}" >> "$CSV"
            QPS_VALS+=("$QPS")
            P99_VALS+=("$P99")
        done

        QPS_MED=$(median "${QPS_VALS[@]}")
        P99_MED=$(median "${P99_VALS[@]}")
        echo ""
        echo ">>> mode=${MODE} workload=${WL}: qps_med=${QPS_MED} p99_med=${P99_MED}"
        echo "${MODE},${WL},${QPS_MED},${P99_MED}" >> "$SUMMARY_CSV"
    done
done

echo ""
echo "=== Summary ==="
column -t -s, "$SUMMARY_CSV" 2>/dev/null || cat "$SUMMARY_CSV"
echo ""
echo "Raw:     $CSV"
echo "Summary: $SUMMARY_CSV"
