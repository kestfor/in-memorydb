# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A distributed in-memory database with strong eventual consistency guarantees, built using:
- **Architecture**: Peer-to-peer (symmetric, no leader)
- **Consistency Model**: Strong eventual consistency via CRDTs
- **Communication**: gRPC with mutual TLS
- **Membership**: SWIM-inspired gossip protocol
- **Data Types**: CRDT types (G-Counter, PN-Counter, LWW-Register, MV-Register)
- **Replication**: Full replication (all nodes store all data), extensible to k-way
- **Persistence**: Append-only log + periodic snapshots

See `RFC.md` for detailed technical specification and `ROADMAP.md` for development plan.

## Build and Development Commands

```bash
# Generate Protocol Buffers
make proto

# Build node and client binaries
make build

# Run all tests
make test

# Run tests with race detector (ALWAYS use for concurrent code)
go test -race ./...

# Run integration tests
make integration-test

# Run benchmarks
make bench

# Lint code
make lint

# Run a single test
go test -v -run TestName ./path/to/package

# Run tests for a specific package
go test -v ./pkg/crdt

# Clean build artifacts
make clean

# Generate TLS certificates for local testing
cd certs && ../scripts/gen-certs.sh

# Start local cluster (3 nodes by default)
./scripts/run-cluster.sh 3

# Run single node
./bin/node --config=configs/node1.yaml

# Use client CLI
./bin/client -server localhost:9090 incr counter1 5
./bin/client -server localhost:9090 get counter1
./bin/client -server localhost:9090 status
```

## Project Architecture

### Directory Structure
```
pkg/
├── crdt/         # CRDT implementations (G-Counter, PN-Counter, LWW/MV-Register)
├── storage/      # Storage engine managing multiple CRDT instances
├── persistence/  # Append-only log, snapshots, recovery
├── membership/   # SWIM-based membership management
├── gossip/       # Gossip protocol for membership + CRDT delta propagation
├── grpc/         # gRPC services (node-to-node + client API)
├── config/       # YAML configuration loading
├── crypto/       # TLS certificate management
└── util/         # Logging and utilities

api/proto/        # Protocol Buffer definitions
cmd/
├── node/         # Node server main
└── client/       # CLI client tool
test/
├── integration/  # Multi-node integration tests
└── chaos/        # Fault injection tests
```

### Core Components

**CRDTs (pkg/crdt/)**:
- All CRDTs implement `Merge()`, `ApplyDelta()`, and version vector tracking
- Deltas are generated on mutations and gossiped to peers
- Mathematical guarantees: commutativity, associativity, idempotence
- ALWAYS test with property-based tests and race detector

**Storage Engine (pkg/storage/)**:
- Manages map of CRDT ID → CRDT instance
- Thread-safe with RWMutex
- Tracks version vectors for anti-entropy
- Delta buffer for gossip (last 100 ops per CRDT)

**Gossip Protocol (pkg/gossip/)**:
- Membership gossip: Every 2 seconds, fanout 3 nodes
- CRDT delta gossip: Piggyback deltas on membership messages
- Anti-entropy: Full state sync every 60 seconds with random peer
- Failure detection: Alive → Suspected (6s) → Dead (10s)

**Persistence (pkg/persistence/)**:
- Append-only log with CRC32 checksums
- Log entries: CRDT deltas, membership changes, snapshot markers
- Snapshots: Protocol Buffer format, every 5 minutes or 500MB log
- Recovery: Load latest snapshot + replay log entries

**gRPC Services (pkg/grpc/)**:
- NodeService: Gossip(), SyncState(), PushDelta(), Ping()
- ClientService: Counter/Register operations, GetClusterStatus()
- All connections use mutual TLS (mTLS)

## Development Workflow

### Adding a New CRDT Type

1. Define CRDT struct in `pkg/crdt/`
2. Implement `CRDT` interface: `Merge()`, `ApplyDelta()`, `Marshal()`, `Unmarshal()`
3. Create delta struct with version vector
4. Add to `CRDTType` enum in `storage/engine.go`
5. Update factory in `storage/factory.go`
6. Add protobuf message in `api/proto/persistence.proto`
7. Write unit tests with property-based testing
8. Test convergence with integration tests

### Testing Guidelines

- **Unit tests**: 100% coverage for CRDT operations
- **Property-based tests**: Use `testing/quick` or `gopter` for CRDT properties
- **Race detector**: ALWAYS run `go test -race` for concurrent code
- **Integration tests**: Multi-node scenarios in `test/integration/`
- **Chaos tests**: Network partitions, node failures, packet loss

### Concurrency Safety

- CRDTs are NOT thread-safe by themselves
- Storage engine provides thread-safety via locks
- All operations go through storage engine, never access CRDTs directly
- Use `-race` flag to detect data races

## Key Design Decisions

1. **Full Replication Initially**: All nodes store all data for simplicity. K-way replication is a planned future enhancement.

2. **Delta-based CRDTs**: Only deltas are gossiped, not full state, for efficiency. Anti-entropy syncs full state periodically.

3. **Version Vectors**: Track causality for conflict detection and anti-entropy synchronization.

4. **Gossip Parameters**: 2s interval, fanout 3 - balance between network overhead and convergence speed.

5. **Persistence Strategy**: Append-only log for durability, snapshots for fast recovery. Trade-off: disk space vs recovery time.

6. **No Byzantine Fault Tolerance**: Assumes trusted cluster environment. Authentication via mTLS prevents unauthorized joins.

## Common Pitfalls

- **Don't access CRDTs directly**: Always go through storage engine for thread-safety
- **Don't forget version vectors**: Every delta must include updated version vector
- **Don't skip anti-entropy**: Gossip may miss messages; anti-entropy ensures convergence
- **Don't ignore checksums**: Always validate log entries during recovery
- **Don't block in gossip**: Use goroutines for sending gossip messages
- **Test partition scenarios**: CRDTs must converge after network heals

## Performance Expectations

Per node (4-core, 16GB RAM):
- Counter operations: ~50,000 ops/sec
- Register operations: ~30,000 ops/sec
- Log writes: >5,000 writes/sec with fsync
- RPC latency: <5ms localhost
- Gossip overhead: 2-5% CPU
- Cluster propagation: 2-6 seconds

## References

- RFC.md: Complete technical specification
- ROADMAP.md: Phase-by-phase implementation plan
- CRDTs: Shapiro et al., "Conflict-free Replicated Data Types"
- SWIM: Das et al., "Scalable Weakly-consistent Infection-style Process Group Membership Protocol"