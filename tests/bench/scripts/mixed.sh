#!/usr/bin/env bash
# mixed.sh — mixed workload (70% get / 30% set), sweep по конкурентности.
# Запуск: ./mixed.sh [port] [rpc] [duration]
# Пример: ./mixed.sh 8081 4 30

set -euo pipefail

PORT="${1:-8081}"
RPC="${2:-4}"
DURATION="${3:-30}"
WARMUP=5
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

CSV="$RESULTS_DIR/mixed_$(date +%Y%m%d_%H%M%S).csv"
echo "conn,workers,qps,p50,p90,p99" > "$CSV"
echo "=== mixed sweep: port=$PORT r=$RPC duration=${DURATION}s ==="

for c in 1 5 10 25 50 100; do
    workers=$(( c * RPC ))
    printf "  c=%-3s workers=%-4s ... " "$c" "$workers"
    out=$("$LUME_BENCH" -p "$PORT" -c "$c" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t mixed 2>/dev/null)
    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    echo "$c,$workers,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "qps=%-8s p99=%s\n" "$qps" "$p99"
done

echo "Saved: $CSV"