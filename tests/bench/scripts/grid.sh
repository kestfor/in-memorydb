#!/usr/bin/env bash
# grid.sh — c×r решётка: c ∈ {1,2,5,10}, r ∈ {50,100,200,400,700,1000}
# Запуск: ./grid.sh [port] [duration]
# Пример: ./grid.sh 8081 30
# Требует запущенного узла Lume и собранного lume-bench в PATH или ../../lume-bench

set -euo pipefail

PORT="${1:-8081}"
DURATION="${2:-30}"
WARMUP=5
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

parse_qps()  { sed "$STRIP_ANSI" | grep 'QPS:' | awk '{print $NF}'; }
parse_p50()  { sed "$STRIP_ANSI" | grep 'Latency:' | sed 's/.*p50=\([^ ]*\).*/\1/'; }
parse_p90()  { sed "$STRIP_ANSI" | grep 'Latency:' | sed 's/.*p90=\([^ ]*\).*/\1/'; }
parse_p99()  { sed "$STRIP_ANSI" | grep 'Latency:' | sed 's/.*p99=\([^ ]*\).*/\1/'; }

run_bench() {
    local c="$1" r="$2" type="$3"
    local workers=$(( c * r ))
    local out
    out=$("$LUME_BENCH" -p "$PORT" -c "$c" -r "$r" -d "$DURATION" -w "$WARMUP" -t "$type" 2>/dev/null)
    local qps p50 p90 p99
    qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
    p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
    p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
    p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
    echo "$c,$r,$workers,$qps,$p50,$p90,$p99"
}

for type in set get; do
    CSV="$RESULTS_DIR/grid_${type}_$(date +%Y%m%d_%H%M%S).csv"
    echo "c,r,workers,qps,p50,p90,p99" > "$CSV"
    echo "=== grid sweep: type=$type port=$PORT duration=${DURATION}s ==="
    for c in 1 2 5 10; do
        for r in 50 100 200 400 700 1000; do
            printf "  c=%-3s r=%-2s ... " "$c" "$r"
            row=$(run_bench "$c" "$r" "$type")
            echo "$row" >> "$CSV"
            qps=$(echo "$row" | cut -d, -f4)
            p99=$(echo "$row" | cut -d, -f7)
            printf "qps=%-8s p99=%s\n" "$qps" "$p99"
        done
    done
    echo "Saved: $CSV"
done