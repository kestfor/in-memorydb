#!/usr/bin/env bash
# pool.sh — sweep по --pool-size: {100, 1000, 10000, 100000, 1000000}.
# Запуск: ./pool.sh [port] [conn] [rpc] [duration]
# Пример: ./pool.sh 8081 10 4 30

set -euo pipefail

PORT="${1:-8081}"
CONN="${2:-10}"
RPC="${3:-4}"
DURATION="${4:-30}"
WARMUP=5
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

CSV="$RESULTS_DIR/pool_$(date +%Y%m%d_%H%M%S).csv"
echo "pool_size,qps,p50,p90,p99" > "$CSV"
echo "=== pool-size sweep: port=$PORT c=$CONN r=$RPC duration=${DURATION}s ==="

for pool in 100 1000 10000 100000 1000000; do
    printf "  pool-size=%-8s ... " "$pool"
    out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t mixed --pool-size "$pool" 2>/dev/null)
    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    echo "$pool,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "qps=%-8s p99=%s\n" "$qps" "$p99"
done

echo "Saved: $CSV"