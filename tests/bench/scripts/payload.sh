#!/usr/bin/env bash
# payload.sh — sweep по --req-size: {64, 256, 1024, 4096, 16384} байт.
# Запуск: ./payload.sh [port] [conn] [rpc] [duration]
# Пример: ./payload.sh 8081 10 4 30

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

CSV="$RESULTS_DIR/payload_$(date +%Y%m%d_%H%M%S).csv"
echo "req_size_bytes,qps,p50,p90,p99" > "$CSV"
echo "=== payload sweep: port=$PORT c=$CONN r=$RPC duration=${DURATION}s ==="

for size in 64 256 1024 4096 16384; do
    printf "  req-size=%-6s ... " "$size"
    out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" \
        -t set --req-size "$size" 2>/dev/null)
    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    echo "$size,$qps,$p50,$p90,$p99" >> "$CSV"
    printf "qps=%-8s p99=%s\n" "$qps" "$p99"
done

echo "Saved: $CSV"