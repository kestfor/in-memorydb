#!/usr/bin/env bash
# node-failure.sh — тест 4.7.1: устойчивость кластера без выделенного лидера.
#
# Кластер фиксированного размера N=5. Нагрузка идёт на node1 (порт 8081).
# Варьируем число одновременно остановленных узлов: 0, 1, 2, 3 (из 4 «чужих»,
# node1 всегда жив, потому что обслуживает клиента).
#
# Каждый сценарий повторяется REPEATS раз, в таблице — медиана.
# Soft-limit: ~15 минут.
#
# Запуск:
#   ./node-failure.sh [conn] [rpc] [duration_per_phase] [repeats]
#   ./node-failure.sh 10 4 20 5

set -euo pipefail

CONN="${1:-10}"
RPC="${2:-4}"
DUR="${3:-20}"
REPEATS="${4:-5}"
WARMUP=3
CLUSTER_SIZE=5
# Сценарии: число остановленных узлов (от 0 до CLUSTER_SIZE-1, но не более 3 чтобы
# гарантировать, что хотя бы один пир жив для репликации).
FAIL_COUNTS=(0 1 2 3)

CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_BENCH="${LUME_BENCH:-lume-bench}"
RESULTS_DIR="$(dirname "$0")/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="${CLUSTER_SIZE}-node"
STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

# median <numbers...> — медиана с awk (подходит для float).
median() {
    printf '%s\n' "$@" | sort -n | awk '
        { a[NR]=$1 }
        END {
            if (NR==0) { print "0"; exit }
            if (NR%2==1) print a[(NR+1)/2]
            else printf "%.4f\n", (a[NR/2]+a[NR/2+1])/2
        }'
}

run_bench() {
    local out qps p99 errs
    out=$("$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" -d "$DUR" -w "$WARMUP" -t mixed 2>/dev/null || true)
    qps=$(echo "$out"  | grep -m1 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p99=$(echo "$out"  | grep -m1 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    errs=$(echo "$out" | grep -m1 -i 'errors\?:' | sed "$STRIP_ANSI" | awk '{print $NF}')
    echo "${qps:-0} ${p99:-0} ${errs:-0}"
}

start_cluster() {
    docker compose -f "$COMPOSE" --profile "$PROFILE" up -d --build --wait >/dev/null
}

stop_cluster() {
    docker compose -f "$COMPOSE" --profile "$PROFILE" down -v >/dev/null 2>&1 || true
}

trap stop_cluster EXIT

CSV="$RESULTS_DIR/node_failure_$(date +%Y%m%d_%H%M%S).csv"
echo "fail_count,alive_nodes,repeat,qps,p99_ms,errors" > "$CSV"

SUMMARY_CSV="$RESULTS_DIR/node_failure_summary_$(date +%Y%m%d_%H%M%S).csv"
echo "fail_count,alive_nodes,qps_median,p99_median_ms,errors_total" > "$SUMMARY_CSV"

echo "=== node-failure: cluster=${CLUSTER_SIZE}, fail_counts=[${FAIL_COUNTS[*]}], repeats=${REPEATS}, dur=${DUR}s ==="

for FC in "${FAIL_COUNTS[@]}"; do
    ALIVE=$(( CLUSTER_SIZE - FC ))
    QPS_VALS=()
    P99_VALS=()
    ERR_TOTAL=0

    for R in $(seq 1 "$REPEATS"); do
        echo ""
        echo "[fail=${FC}, repeat=${R}/${REPEATS}] starting cluster..."
        start_cluster

        if [ "$FC" -gt 0 ]; then
            # Останавливаем node2..node{FC+1} (node1 трогать нельзя — на ней клиентская нагрузка).
            for i in $(seq 2 $((FC + 1))); do
                docker stop "lume-node${i}" >/dev/null
            done
            sleep 2  # дать SWIM засечь падение
        fi

        read -r QPS P99 ERRS < <(run_bench)
        echo "    qps=${QPS}  p99=${P99}  errors=${ERRS}"

        echo "fail=${FC},alive=${ALIVE},${R},${QPS},${P99},${ERRS}" >> "$CSV"
        QPS_VALS+=("$QPS")
        P99_VALS+=("$P99")
        ERR_TOTAL=$(( ERR_TOTAL + ${ERRS%.*} ))

        stop_cluster
    done

    QPS_MED=$(median "${QPS_VALS[@]}")
    P99_MED=$(median "${P99_VALS[@]}")
    echo ""
    echo ">>> fail=${FC} alive=${ALIVE}: qps_med=${QPS_MED} p99_med=${P99_MED} errs_total=${ERR_TOTAL}"
    echo "${FC},${ALIVE},${QPS_MED},${P99_MED},${ERR_TOTAL}" >> "$SUMMARY_CSV"
done

echo ""
echo "=== Summary ==="
column -t -s, "$SUMMARY_CSV" 2>/dev/null || cat "$SUMMARY_CSV"
echo ""
echo "Raw:     $CSV"
echo "Summary: $SUMMARY_CSV"
