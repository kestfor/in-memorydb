<p align="center">
  <h1 align="center">Lume</h1>
  <p align="center">
    <strong>Distributed, in-memory, CRDT-based key-value store written in Go</strong>
  </p>
</p>

<p align="center">
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.26.1-00ADD8?style=flat&logo=go&logoColor=white" alt="Go Version"></a>
  <a href="https://github.com/kestfor/in-memorydb/actions"><img src="https://img.shields.io/github/actions/workflow/status/kestfor/in-memorydb/go.yml?branch=main&style=flat&logo=github" alt="Build Status"></a>
  <a href="https://goreportcard.com/report/github.com/kestfor/in-memorydb"><img src="https://goreportcard.com/badge/github.com/kestfor/in-memorydb" alt="Go Report Card"></a>
  <a href="https://deepwiki.com/kestfor/in-memorydb"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg?style=flat" alt="License"></a>
</p>

Lume is an experimental, masterless, distributed in-memory database. Every node is
equal: clients can read and write to any node, updates propagate to the rest of the
cluster through gossip, and conflicts are resolved automatically because the stored
values are **CRDTs** (Conflict-free Replicated Data Types). The result is an
**AP** store (in CAP terms) that stays available for reads and writes during network
partitions and converges to a single, consistent state once nodes can talk again.

The repository contains everything needed to run, test, and benchmark such a cluster:
the gRPC server, a CLI client, a load generator, a benchmark harness, a TLS
certificate tool, Docker/cluster configs, an observability stack, and tooling that
compares Lume against other databases.

> **Why "experimental"?** Lume started as a graduation project and is a playground for
> exploring CRDTs, hybrid logical clocks, gossip replication and anti-entropy. It is
> not production-hardened.

## Table of contents

- [Features](#features)
- [How it works](#how-it-works)
- [Data model](#data-model)
- [Requirements](#requirements)
- [Build](#build)
- [Quick start](#quick-start)
- [Running a cluster with Docker](#running-a-cluster-with-docker)
- [CLI usage](#cli-usage)
- [Load generation and benchmarking](#load-generation-and-benchmarking)
- [Configuration](#configuration)
- [TLS certificates](#tls-certificates)
- [Observability](#observability)
- [Testing and quality checks](#testing-and-quality-checks)
- [Repository layout](#repository-layout)
- [Further reading](#further-reading)
- [License](#license)

## Features

- **CRDT data model** — `PN-Counter` (increment/decrement counter) and
  `LWW-Register` (last-writer-wins byte register), so concurrent writes merge
  deterministically without coordination.
- **Hybrid Logical Clocks (HLC)** for ordering events and breaking ties in
  last-writer-wins resolution without relying on synchronized wall clocks.
- **Masterless replication** — any node accepts reads and writes; there is no
  single point of failure and no leader election on the hot path.
- **Gossip-based propagation** — local updates are buffered and pushed to a random
  subset of peers each round (configurable fanout, batch size and worker pool).
- **Anti-entropy synchronization** — nodes periodically reconcile state to repair
  missed or dropped updates, guaranteeing eventual convergence.
- **Sharded in-memory engine** with a configurable number of shards for parallelism
  and tombstone-based deletion with a garbage-collection grace period.
- **WAL-backed durability** — optional write-ahead log with configurable flush
  interval, sync mode, batching and segment rotation.
- **Cluster membership** built on HashiCorp `memberlist` (SWIM-style failure
  detection and gossip-based membership).
- **Pluggable TLS** — `disabled`, `internal` (node-to-node) or `full` (+ client TLS),
  with a built-in certificate authority tool.
- **OpenTelemetry tracing** and a ready-to-run Prometheus/Grafana/Tempo stack.

## How it works

```text
        client (gRPC)                       client (gRPC)
             │                                    │
             ▼                                    ▼
   ┌──────────────────┐   gossip / anti-entropy  ┌──────────────────┐
   │      Node A      │◄────────────────────────►│      Node B      │
   │ ───────────────  │                          │ ───────────────  │
   │ sharded engine   │   memberlist (SWIM)      │ sharded engine   │
   │ CRDT values + HLC│◄────────────────────────►│ CRDT values + HLC│
   │ updates buffer   │                          │ updates buffer   │
   │ WAL (optional)   │                          │ WAL (optional)   │
   └──────────────────┘                          └──────────────────┘
```

1. A client issues `Set`, `Get`, `Delete` or `Apply` against **any** node over gRPC.
2. The node applies the operation to its local sharded engine, stamps it with an HLC
   timestamp, appends it to the WAL (if persistence is enabled) and writes it into an
   updates buffer.
3. A gossip worker drains the buffer and pushes batches of updates to a random set of
   peers; receiving nodes merge them into their own CRDT state.
4. A periodic anti-entropy round reconciles any divergence that gossip missed, so all
   nodes eventually converge to the same state.
5. `memberlist` tracks which nodes are alive and disseminates membership changes.

## Data model

Lume is a key-value store where every value is a CRDT. The type is fixed at creation
time and determines how concurrent updates merge:

| Type           | Proto enum            | Operations              | Conflict resolution                          |
| -------------- | --------------------- | ----------------------- | -------------------------------------------- |
| `PN-Counter`   | `TYPE_PN_COUNTER`     | increment, decrement    | per-node increment/decrement sums are merged |
| `LWW-Register` | `TYPE_LWW_REGISTER`   | set (arbitrary bytes)   | highest HLC timestamp wins                   |

The gRPC service (see [`api/lume/lume.proto`](./api/lume/lume.proto)) exposes four RPCs:

| RPC      | Purpose                                           |
| -------- | ------------------------------------------------- |
| `Set`    | create a key with a given CRDT type               |
| `Get`    | read a key's current value and type               |
| `Delete` | tombstone a key                                   |
| `Apply`  | apply an operation (inc/dec a counter, set a register) |

## Requirements

- **Go** `1.26.1`
- **Docker** and **Docker Compose** for container-based runs
- **CGO** enabled for the test suite (`CGO_ENABLED=1`, required by the race detector)
- [`buf`](https://buf.build/) only if you need to regenerate protobuf code
  (available as a Go tool dependency, see below)

## Build

```bash
go build -o bin/lume       ./cmd/grpc      # server
go build -o bin/lume-cli   ./cmd/lume-cli  # interactive CLI client
go build -o bin/lume-bench ./cmd/lume-bench # benchmark / load generator
go build -o bin/lume-load  ./cmd/lume-load # bulk key loader
go build -o bin/lume-ca    ./cmd/lume-ca   # TLS certificate authority tool
```

## Quick start

Run a single node with the ready-made config and talk to it with the CLI:

```bash
# 1. build the server and CLI
go build -o bin/lume     ./cmd/grpc
go build -o bin/lume-cli ./cmd/lume-cli

# 2. start a node (client gRPC API on :8080 by default)
./bin/lume --config ./docs/node-template.yaml

# 3. in another terminal, create and mutate some values
./bin/lume-cli --server localhost:8080 set visits --type counter
./bin/lume-cli --server localhost:8080 apply inc visits 5
./bin/lume-cli --server localhost:8080 get visits        # -> Value: 5
```

A node exposes three ports (defaults shown):

| Port   | Config key         | Purpose                          |
| ------ | ------------------ | -------------------------------- |
| `8080` | `node.port`        | client gRPC API                  |
| `8081` | `gossip.port`      | gossip / update propagation      |
| `8082` | `membership.port`  | memberlist membership traffic    |

Start from the annotated template in
[`docs/node-template.yaml`](./docs/node-template.yaml) or one of the ready configs in
[`node-configs`](./node-configs).

## Running a cluster with Docker

Single node (uses the root [`docker-compose.yaml`](./docker-compose.yaml) and mounts
[`node-configs/first-peer.yaml`](./node-configs/first-peer.yaml)):

```bash
make docker-up        # start
make docker-down      # stop
```

Multi-node cluster (uses [`cluster/docker-compose.yaml`](./cluster/docker-compose.yaml)
with configs from [`node-configs`](./node-configs)):

```bash
make docker-up-cluster    # start
make docker-down-cluster  # stop
```

## CLI usage

`lume-cli` talks to the gRPC API directly. Pass the target node with `--server`
(`-s`); the default is `localhost:9090`, so set it explicitly when your node listens
on `:8080`.

```bash
# counters
./bin/lume-cli -s localhost:8080 set visits --type counter
./bin/lume-cli -s localhost:8080 apply inc visits 1
./bin/lume-cli -s localhost:8080 apply dec visits 1
./bin/lume-cli -s localhost:8080 get visits

# registers
./bin/lume-cli -s localhost:8080 set greeting --type register
./bin/lume-cli -s localhost:8080 apply register greeting "hello from lume"
./bin/lume-cli -s localhost:8080 get greeting
./bin/lume-cli -s localhost:8080 delete greeting

# shortcut: `set <key> <value>` auto-detects the type
#   numeric value -> counter incremented by that amount
#   text value    -> register set to that string
./bin/lume-cli -s localhost:8080 set visits 10
./bin/lume-cli -s localhost:8080 set greeting "hello"
```

Command groups (implemented in [`cmd/lume-cli/main.go`](./cmd/lume-cli/main.go)):

| Command                       | Description                                    |
| ----------------------------- | ---------------------------------------------- |
| `set <key> [value]`           | create a CRDT (`--type counter\|register`) or auto-detect from value |
| `get <key>`                   | read a value and its type                       |
| `delete <key>`                | delete a key                                    |
| `apply inc <key> <n>`         | increment a counter                             |
| `apply dec <key> <n>`         | decrement a counter                             |
| `apply register <key> <val>`  | set a register                                  |

## Load generation and benchmarking

Two tools generate traffic against a running cluster:

**`lume-bench`** runs a fixed-duration workload (`get`, `set` or `mixed`) and writes
CPU/heap profiles for analysis:

```bash
# -p ports, -c connections/port, -r RPCs/connection, -t workload,
# -d duration(s), -w warm-up(s), -o output dir
./bin/lume-bench -p 8080 -c 8 -r 32 -t mixed -d 30 -w 5 -o ./bench-out
```

Key flags (run `lume-bench --help` for the full list): `--ports/-p`, `--conn/-c`,
`--rpc/-r`, `--warmup/-w`, `--duration/-d`, `--type/-t`, `--req-size`, `--pool-size`,
`--max-rps`, `--name/-n`, `--out/-o`.

**`lume-load`** bulk-inserts a fixed number of random keys and then exits (handy for
seeding a cluster before measuring reads):

```bash
./bin/lume-load -s localhost:8080 -n 1000000 -t register --value-size 64 -c 32
./bin/lume-load -s localhost:8080 -n 100000  -t counter  -c 16
```

The repository also ships:

- benchmark shell scripts in [`tests/bench/scripts`](./tests/bench/scripts)
- published benchmark result files in [`tests/bench/results`](./tests/bench/results)
- database comparison tooling in [`tests/comparison`](./tests/comparison) (with a
  Make target: `make docker-up-comparison` / `make docker-down-comparison`)
- exported comparison charts and CSV results in
  [`tests/comparison/test_results`](./tests/comparison/test_results)

## Configuration

Nodes are configured with a YAML file passed via `--config`. The fully annotated
template lives in [`docs/node-template.yaml`](./docs/node-template.yaml). Top-level
sections:

| Section        | What it controls                                                        |
| -------------- | ----------------------------------------------------------------------- |
| `node`         | node id, client gRPC bind address/port, max concurrent streams          |
| `gossip`       | gossip bind address/port, round interval, fanout, retries, batch sizes  |
| `membership`   | memberlist port and advertise address                                   |
| `seeds`        | known seed nodes used to join the cluster                               |
| `persistence`  | WAL on/off, path, segment threshold, flush interval, sync mode, batching |
| `engine`       | number of shards and tombstone garbage-collection grace period          |
| `buffer`       | local updates buffer size, read interval and peek batch size            |
| `transport`    | max gRPC message size and pull batch size                               |
| `security`     | TLS mode (`disabled`/`internal`/`full`) and certificate/key paths       |
| `trace`        | OpenTelemetry on/off and OTLP HTTP endpoint                             |

All duration values use Go's duration format (`500ms`, `5s`, `1m`, `1h`).

## TLS certificates

`lume-ca` ([`cmd/lume-ca`](./cmd/lume-ca)) generates a local CA and node certificates
for TLS-enabled deployments:

```bash
./bin/lume-ca init-ca --out-dir ./certs
./bin/lume-ca issue --name node1 \
  --ca-cert ./certs/ca.crt --ca-key ./certs/ca.key \
  --out-dir ./certs/node1
```

Point the `security` section of your node config at the generated files and choose a
`mode` (`internal` for node-to-node TLS, `full` to also require client TLS).

## Observability

A local observability stack (Prometheus, Grafana, Tempo) lives in
[`observability`](./observability):

```bash
cd observability
docker compose up -d
```

Included configs:

- Grafana datasources — [`observability/grafana/grafana-datasources.yaml`](./observability/grafana/grafana-datasources.yaml)
- Prometheus — [`observability/prometheus/prometheus.yaml`](./observability/prometheus/prometheus.yaml)
- Tempo — [`observability/tempo/tempo.yaml`](./observability/tempo/tempo.yaml)

Enable tracing by setting `trace.enabled: true` and pointing `trace.endpoint` at the
OTLP HTTP collector.

## Testing and quality checks

Developer targets are defined in the [`Makefile`](./Makefile):

```bash
make test      # gotestsum with -race, vet and repeated runs (CGO_ENABLED=1)
make lint      # golangci-lint
make bench     # go test -bench=. -benchmem ./...
make format    # go fmt ./...
make protos    # regenerate gRPC code from the proto definitions via buf
```

Package-level tests cover storage, CRDTs, membership, the WAL and anti-entropy.

## Repository layout

```text
.
├── api/lume          # protobuf definitions and generated gRPC code
├── cmd/grpc          # main Lume server
├── cmd/lume-cli      # CLI client
├── cmd/lume-bench    # benchmark / load generator with profiling
├── cmd/lume-load     # bulk key loader
├── cmd/lume-ca       # CA and node certificate tool
├── cmd/mock          # generated mocks
├── cluster           # docker-compose for a multi-node cluster
├── docs              # design notes, config template, graduation paper, plans
├── node-configs      # sample node configs for docker / local runs
├── observability     # Prometheus / Grafana / Tempo setup
├── pkg/crdt          # CRDT implementations and HLC
├── pkg/gossip        # gossip transport and buffering
├── pkg/membership    # cluster membership
├── pkg/storage       # engine, WAL, version managers, updates buffer, anti-entropy
├── pkg/transport     # gRPC transport layer
├── pkg/observability # tracing / spans
├── pkg/tlsx          # TLS helpers
├── pkg/configx       # configuration loading
└── tests             # comparison, cluster, convergence and fault-tolerance tests
```

## Further reading

- [Node configuration template](./docs/node-template.yaml)
- [Key-based anti-entropy design](./docs/key-based-anti-entropy-design.md)
- [Version manager v2 optimization plan](./docs/version_manager_v2_optimization_plan.md)
- [Performance analysis notes](./docs/future/performance_analysis.md)
- [Engine v1 notes](./pkg/storage/engine/v1/Readme.md)
- [Comparison monitoring README](./tests/comparison/monitoring/README.md)

## License

Released under the MIT License — see [LICENSE](./LICENSE).