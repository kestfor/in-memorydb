#!/usr/bin/env bash
# gossip-overhead.sh — тест 1.1: сравнение throughput одного узла vs кластер.
# Запуск: ./gossip-overhead.sh <conn> <rpc> [duration]
# Пример: ./gossip-overhead.sh 10 4 60
#
# Прогоняет lume-bench для конфигураций: 1-node, 2-node, 3-node.
# Типы: set и get.

set -euo pipefail

CONN="${1:?Usage: $0 <conn> <rpc> [duration]}"
RPC="${2:?}"
DURATION="${3:-60}"
WARMUP=10
PORT=8081
LUME_BENCH="${LUME_BENCH:-lume-bench}"
CLUSTER_DIR="$(dirname "$0")/../../cluster"
RESULTS_DIR="$(dirname "$0")/../results"
mkdir -p "$RESULTS_DIR"

STRIP_ANSI='s/\x1b\[[0-9;]*m//g'
COMPOSE="$CLUSTER_DIR/docker-compose.yaml"
PROJECT="lume_gossip_oh"

CSV="$RESULTS_DIR/gossip_overhead_$(date +%Y%m%d_%H%M%S).csv"
echo "nodes,type,qps,p50,p90,p99" > "$CSV"
echo "=== gossip-overhead sweep: c=$CONN r=$RPC duration=${DURATION}s ==="

trap 'docker compose -f "$COMPOSE" -p "$PROJECT" down -v 2>/dev/null || true' EXIT

for nodes in 1 2 3; do
    profile="${nodes}-node"
    printf "\n--- %d-node cluster ---\n" "$nodes"

    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" up -d --build --wait 2>/dev/null
    echo "    cluster ready"

    for op in set get; do
        printf "  op=%-4s ... " "$op"
        out=$("$LUME_BENCH" -p "$PORT" -c "$CONN" -r "$RPC" \
            -d "$DURATION" -w "$WARMUP" -t "$op" --pool-size 100000 2>/dev/null)

        qps=$(echo "$out" | grep 'QPS:'     | sed "$STRIP_ANSI" | awk '{print $NF}')
        p50=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p50=\([^ ]*\).*/\1/')
        p90=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p90=\([^ ]*\).*/\1/')
        p99=$(echo "$out" | grep 'Latency:' | sed "$STRIP_ANSI" | sed 's/.*p99=\([^ ]*\).*/\1/')

        echo "$nodes,$op,$qps,$p50,$p90,$p99" >> "$CSV"
        printf "qps=%-8s p50=%s p90=%s p99=%s\n" "$qps" "$p50" "$p90" "$p99"
    done

    docker compose -f "$COMPOSE" --profile "$profile" -p "$PROJECT" down -v 2>/dev/null
    echo "    cluster stopped"
done

echo ""
echo "Saved: $CSV"
