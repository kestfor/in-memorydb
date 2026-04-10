#!/usr/bin/env bash
# node-failure.sh — тест 2.1: останавливает узел во время нагрузки, фиксирует ошибки и p99.
# Запуск: ./node-failure.sh [conn] [rpc] [duration_before_stop] [duration_after_restart]
# Пример: ./node-failure.sh 10 4 30 30
#
# Топология: 3 узла (docker-compose-3.yaml).
# lume-bench бьёт в node1 (8081). На 30-й секунде останавливаем node2.
# Ждём 30 с. Запускаем node2. Ждём восстановления членства.

set -euo pipefail

CONN="${1:-10}"
RPC="${2:-4}"
DUR_PRE="${3:-30}"   # секунды нагрузки до остановки узла
DUR_POST="${4:-30}"  # секунды нагрузки после рестарта узла
WARMUP=5
CLUSTER_DIR="$(dirname "$0")/../cluster"
LUME_BENCH="${LUME_BENCH:-lume-bench}"
RESULTS_DIR="$(dirname "$0")/../bench/results"
mkdir -p "$RESULTS_DIR"

COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROFILE="3-node"
STRIP_ANSI='s/\x1b\[[0-9;]*m//g'

echo "=== node-failure test: 3-node cluster, stop node2 during bench ==="

echo "[1] Starting 3-node cluster..."
docker compose -f "$COMPOSE" --profile "$PROFILE" up -d --build --wait
echo "    cluster ready"

echo "[2] Baseline bench (${DUR_PRE}s, node2 alive)..."
out_pre=$("$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" -d "$DUR_PRE" -w "$WARMUP" -t mixed 2>/dev/null)
qps_pre=$(echo "$out_pre" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
p99_pre=$(echo "$out_pre" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
echo "    qps=$qps_pre  p99=$p99_pre"

echo "[3] Stopping lume-node2..."
docker stop lume-node2
STOP_TIME=$(date +%s)
echo "    node2 stopped at $(date)"

echo "[4] Bench under failure (${DUR_PRE}s)..."
out_fail=$("$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" -d "$DUR_PRE" -w 0 -t mixed 2>/dev/null)
qps_fail=$(echo "$out_fail" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
p99_fail=$(echo "$out_fail" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
echo "    qps=$qps_fail  p99=$p99_fail"

echo "[5] Restarting lume-node2..."
docker start lume-node2
RESTART_TIME=$(date +%s)

# Ждём, пока node2 снова станет healthy
echo "    waiting for node2 to rejoin..."
for i in $(seq 1 30); do
    status=$(docker inspect --format='{{.State.Health.Status}}' lume-node2 2>/dev/null || echo "unknown")
    if [ "$status" = "healthy" ]; then
        REJOIN_TIME=$(date +%s)
        echo "    node2 healthy after $(( REJOIN_TIME - RESTART_TIME ))s"
        break
    fi
    sleep 2
done

echo "[6] Post-recovery bench (${DUR_POST}s)..."
out_post=$("$LUME_BENCH" -p 8081 -c "$CONN" -r "$RPC" -d "$DUR_POST" -w "$WARMUP" -t mixed 2>/dev/null)
qps_post=$(echo "$out_post" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
p99_post=$(echo "$out_post" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')
echo "    qps=$qps_post  p99=$p99_post"

echo ""
echo "=== Summary ==="
echo "  baseline:    qps=$qps_pre   p99=$p99_pre"
echo "  under fail:  qps=$qps_fail  p99=$p99_fail"
echo "  post-recover:qps=$qps_post  p99=$p99_post"

CSV="$RESULTS_DIR/node_failure_$(date +%Y%m%d_%H%M%S).csv"
echo "phase,qps,p99" > "$CSV"
echo "baseline,$qps_pre,$p99_pre"       >> "$CSV"
echo "failure,$qps_fail,$p99_fail"      >> "$CSV"
echo "recovery,$qps_post,$p99_post"     >> "$CSV"
echo "Saved: $CSV"

echo ""
echo "[7] Tearing down cluster..."
docker compose -f "$COMPOSE" --profile "$PROFILE" down -v