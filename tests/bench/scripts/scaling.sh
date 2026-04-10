#!/usr/bin/env bash
# scaling.sh — горизонтальное масштабирование: N параллельных lume-bench на N узлов.
# Запуск: ./scaling.sh <nodes> <conn_per_node> <rpc_per_conn> [duration]
# Пример: ./scaling.sh 3 10 4 30
#   nodes         — число узлов (порты 8081..808N)
#   conn_per_node — -c для каждого lume-bench
#   rpc_per_conn  — -r для каждого lume-bench
# Требует N запущенных узлов на портах 8081..808N и собранного lume-bench.

set -euo pipefail

NODES="${1:?Usage: $0 <nodes> <conn> <rpc> [duration]}"
CONN="${2:?}"
RPC="${3:?}"
DURATION="${4:-30}"
WARMUP=5
LUME_BENCH="${LUME_BENCH:-../../lume-bench}"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

TMPDIR_BENCH=$(mktemp -d)
trap 'rm -rf "$TMPDIR_BENCH"' EXIT

echo "=== scaling sweep: nodes=$NODES conn=$CONN rpc=$RPC duration=${DURATION}s ==="

# Запускаем N бенчей параллельно, каждый на свой порт
for i in $(seq 1 "$NODES"); do
    port=$(( 8080 + i ))
    outfile="$TMPDIR_BENCH/node${i}.out"
    "$LUME_BENCH" -p "$port" -c "$CONN" -r "$RPC" -d "$DURATION" -w "$WARMUP" -t mixed \
        > "$outfile" 2>/dev/null &
done
wait

# Агрегируем результаты
total_qps=0
p50_sum=0
p99_max=0
count=0

for i in $(seq 1 "$NODES"); do
    outfile="$TMPDIR_BENCH/node${i}.out"
    [ -f "$outfile" ] || continue

    qps=$(grep 'QPS:'     "$outfile" | sed "$STRIP_ANSI" | awk '{print $NF}' | tr -d '\r')
    p50=$(grep 'Latency:' "$outfile" | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/' | tr -d '\r')
    p99=$(grep 'Latency:' "$outfile" | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/' | tr -d '\r')

    echo "  node$i: qps=$qps p50=$p50 p99=$p99"

    # Суммируем QPS (целое для awk)
    total_qps=$(awk -v a="$total_qps" -v b="$qps" 'BEGIN{printf "%.0f", a+b}')
    p50_sum=$(awk -v a="$p50_sum" -v b="$qps" 'BEGIN{printf "%.0f", a+b}')
    count=$(( count + 1 ))

    # p99_max: сравниваем строки длины (как числа без единиц сложно, храним первый наибольший)
    # Запишем p99 как строку, лучший эвристик — длиннее строка = больше значение
    if [ ${#p99} -gt ${#p99_max} ] || ( [ ${#p99} -eq ${#p99_max} ] && [[ "$p99" > "$p99_max" ]] ); then
        p99_max="$p99"
    fi
done

# Базовый QPS (1 узел) берём из первого прогона
baseline_qps=$(grep 'QPS:' "$TMPDIR_BENCH/node1.out" | sed "$STRIP_ANSI" | awk '{print $NF}' | tr -d '\r')
scaling_factor=$(awk -v t="$total_qps" -v b="$baseline_qps" 'BEGIN{if(b>0) printf "%.2f", t/b; else print "N/A"}')
p50_avg=$(awk -v s="$p50_sum" -v n="$count" 'BEGIN{if(n>0) printf "%.0f", s/n; else print "0"}')

echo ""
echo "  total_qps=$total_qps  scaling_factor=$scaling_factor  p50_avg=${p50_avg}  p99_max=$p99_max"

CSV="$RESULTS_DIR/scaling_${NODES}nodes_$(date +%Y%m%d_%H%M%S).csv"
echo "nodes,total_qps,scaling_factor,p50_avg,p99_max" > "$CSV"
echo "$NODES,$total_qps,$scaling_factor,$p50_avg,$p99_max" >> "$CSV"
echo "Saved: $CSV"