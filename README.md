<p align="center">
  <h1 align="center">Lume</h1>
  <p align="center">
    <strong>Distributed in-memory CRDT store in Go</strong>
  </p>
</p>

<p align="center">
  <a href="https://golang.org/"><img src="https://img.shields.io/badge/Go-1.26.1-00ADD8?style=flat&logo=go&logoColor=white" alt="Go Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg?style=flat" alt="License"></a>
</p>

Lume is an experimental distributed in-memory database built around CRDTs, HLC timestamps, gossip replication, and WAL-backed durability. The repository contains the server, CLI client, benchmark client, TLS certificate utility, cluster/docker configs, and comparison tooling used for performance experiments.

## What is in this branch

- gRPC server in [`cmd/grpc`](./cmd/grpc)
- CLI client in [`cmd/lume-cli`](./cmd/lume-cli)
- benchmark client in [`cmd/lume-bench`](./cmd/lume-bench)
- TLS certificate helper in [`cmd/lume-ca`](./cmd/lume-ca)
- CRDT implementations in [`pkg/crdt`](./pkg/crdt)
- storage engine, version managers, WAL, update buffers, and anti-entropy logic in [`pkg/storage`](./pkg/storage)
- gossip and membership layers in [`pkg/gossip`](./pkg/gossip) and [`pkg/membership`](./pkg/membership)
- docker setups for a single node and multi-node cluster in [`docker-compose.yaml`](./docker-compose.yaml) and [`cluster/docker-compose.yaml`](./cluster/docker-compose.yaml)
- comparison and fault-tolerance test tooling in [`tests/comparison`](./tests/comparison), [`tests/cluster`](./tests/cluster), and [`tests/fault-tolerance`](./tests/fault-tolerance)
- observability stack in [`observability`](./observability)

## Core capabilities

- CRDT data model with `PN-Counter` and `LWW-Register`
- Hybrid Logical Clock timestamps for conflict resolution
- sharded in-memory engine
- gossip-based replication
- anti-entropy synchronization for state repair
- WAL-backed persistence with configurable flush and sync behavior
- node membership based on `memberlist`
- optional TLS modes and certificate generation via `lume-ca`
- OpenTelemetry tracing hooks

## Repository layout

```text
.
├── api/lume                    # protobuf definitions and generated gRPC code
├── cmd/grpc                    # main Lume server
├── cmd/lume-cli                # CLI client
├── cmd/lume-bench              # load generator / benchmark client
├── cmd/lume-ca                 # CA and node certificate tool
├── cluster                     # docker-compose for multi-node cluster
├── docs                        # design notes, config template, plans
├── node-configs                # sample node configs for docker runs
├── observability               # Prometheus / Grafana / Tempo setup
├── pkg/crdt                    # CRDT implementations
├── pkg/gossip                  # gossip transport and buffering
├── pkg/membership              # cluster membership
├── pkg/storage                 # engine, WAL, version managers, anti-entropy
└── tests                       # comparison, cluster, convergence and fault tests
```

## Requirements

- Go `1.26.1`
- Docker and Docker Compose for container-based runs
- `buf` if you need to regenerate protobuf code

## Build

```bash
go build -o bin/lume ./cmd/grpc
go build -o bin/lume-cli ./cmd/lume-cli
go build -o bin/lume-bench ./cmd/lume-bench
go build -o bin/lume-ca ./cmd/lume-ca
```

## Run a single node

Use the config template in [`docs/node-template.yaml`](./docs/node-template.yaml) or one of the ready configs in [`node-configs`](./node-configs).

```bash
go build -o bin/lume ./cmd/grpc
./bin/lume --config ./docs/node-template.yaml
```

The current config template exposes:

- client gRPC API on `node.port` (default `8080`)
- gossip traffic on `gossip.port` (default `8081`)
- membership traffic on `membership.port` (default `8082`)

## Run with Docker

Single node:

```bash
make docker-up
```

This uses the root [`docker-compose.yaml`](./docker-compose.yaml) and mounts [`node-configs/first-peer.yaml`](./node-configs/first-peer.yaml).

Multi-node cluster:

```bash
make docker-up-cluster
```

This uses [`cluster/docker-compose.yaml`](./cluster/docker-compose.yaml) together with configs from [`node-configs`](./node-configs).

To stop containers:

```bash
make docker-down
make docker-down-cluster
```

## CLI usage

The CLI talks to the gRPC API directly.

```bash
./bin/lume-cli --server localhost:8080 set visits --type counter
./bin/lume-cli --server localhost:8080 apply inc visits 1
./bin/lume-cli --server localhost:8080 get visits

./bin/lume-cli --server localhost:8080 set greeting "hello"
./bin/lume-cli --server localhost:8080 apply register greeting "hello from lume"
./bin/lume-cli --server localhost:8080 delete greeting
```

Available command groups are implemented in [`cmd/lume-cli/main.go`](./cmd/lume-cli/main.go):

- `set`
- `get`
- `delete`
- `apply inc`
- `apply dec`
- `apply register`

## Benchmarking

The benchmark client is in [`cmd/lume-bench`](./cmd/lume-bench). It supports `get`, `set`, and `mixed` workloads, configurable connection counts, RPC parallelism, warmup, duration, request size, key pool size, and optional rate limiting.

Example:

```bash
./bin/lume-bench -p 8080 -c 8 -r 32 -t mixed -d 30 -w 5 -o ./bench-out
```

This writes CPU and heap profiles into the selected output directory.

The repository also includes:

- benchmark shell scripts in [`tests/bench/scripts`](./tests/bench/scripts)
- published benchmark result files in [`tests/bench/results`](./tests/bench/results)
- database comparison tooling in [`tests/comparison`](./tests/comparison)
- exported comparison charts and CSV results in [`tests/comparison/test_results`](./tests/comparison/test_results)

## Testing and quality checks

The main developer targets are defined in [`Makefile`](./Makefile):

```bash
make test
make lint
make bench
make format
make protos
```

There are also package-level tests across storage, CRDT, membership, WAL, and anti-entropy modules.

## TLS certificates

[`cmd/lume-ca`](./cmd/lume-ca) generates a local CA and node certificates for TLS-enabled deployments.

```bash
./bin/lume-ca init-ca --out-dir ./certs
./bin/lume-ca issue --name node1 --ca-cert ./certs/ca.crt --ca-key ./certs/ca.key --out-dir ./certs/node1
```

Certificate paths and security mode are configured through [`docs/node-template.yaml`](./docs/node-template.yaml).

## Observability

The repository contains a local observability stack in [`observability`](./observability):

```bash
cd observability
docker compose up -d
```

Included configs:

- Grafana datasource config in [`observability/grafana/grafana-datasources.yaml`](./observability/grafana/grafana-datasources.yaml)
- Prometheus config in [`observability/prometheus/prometheus.yaml`](./observability/prometheus/prometheus.yaml)
- Tempo config in [`observability/tempo/tempo.yaml`](./observability/tempo/tempo.yaml)

## Useful documents

- [Node configuration template](./docs/node-template.yaml)
- [Key-based anti-entropy design](./docs/key-based-anti-entropy-design.md)
- [Version manager v2 optimization plan](./docs/version_manager_v2_optimization_plan.md)
- [Performance analysis notes](./docs/future/performance_analysis.md)
- [Engine v1 notes](./pkg/storage/engine/v1/Readme.md)
- [Comparison monitoring README](./tests/comparison/monitoring/README.md)

## License

MIT, see [LICENSE](./LICENSE).
