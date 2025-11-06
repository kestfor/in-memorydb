# RFC: Distributed In-Memory Database with Strong Eventual Consistency

## Status
Draft - Version 0.1

## Abstract

This document specifies the design and implementation of a distributed in-memory database system optimized for high availability and strong eventual consistency. The system employs a peer-to-peer architecture where all nodes are equal participants, utilizing gossip protocols for membership management and CRDT (Conflict-free Replicated Data Types) delta propagation for data synchronization.

## 1. Motivation

Traditional distributed databases often rely on leader-based consensus protocols (Raft, Paxos) which can create availability bottlenecks and single points of failure. This system addresses these limitations by:

- **High Availability**: No single point of failure; all nodes are equal peers
- **Partition Tolerance**: Nodes continue operating during network partitions
- **Eventual Consistency**: Strong mathematical guarantees via CRDTs
- **Scalability**: Gossip-based communication scales efficiently
- **Zero Downtime**: Rolling updates and node failures handled gracefully

## 2. System Architecture

### 2.1 Node Architecture

Each node in the cluster contains the following components:

```
┌─────────────────────────────────────────────────────────────┐
│                        Node Instance                         │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────────┐  │
│  │ gRPC Client  │  │ Gossip Layer │  │  CRDT Storage   │  │
│  │  API Server  │  │  - Membership│  │   - G-Counter   │  │
│  │              │  │  - Delta Sync│  │   - PN-Counter  │  │
│  │              │  │  - Anti-Ent. │  │   - LWW-Register│  │
│  └──────┬───────┘  └──────┬───────┘  │   - MV-Register │  │
│         │                 │          └────────┬────────┘  │
│         └────────┬────────┘                   │           │
│                  │                            │           │
│         ┌────────┴────────────────────────────┴────────┐  │
│         │           Storage Engine Core                │  │
│         │  - Version Vectors                           │  │
│         │  - Delta Generation                          │  │
│         │  - Conflict Resolution                       │  │
│         └──────────────────┬───────────────────────────┘  │
│                            │                              │
│         ┌──────────────────┴───────────────────────────┐  │
│         │         Persistence Layer                    │  │
│         │  - Append-Only Log (WAL)                     │  │
│         │  - Snapshot Manager                          │  │
│         │  - Recovery Coordinator                      │  │
│         └──────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Cluster Topology

The system employs a **symmetric peer-to-peer topology** where:

- All nodes have equal responsibilities
- No designated leader or coordinator
- Each node maintains a partial view of cluster membership
- Membership information propagates via gossip
- Data replication: Initially N-way (all nodes), extensible to K-way replication

```
        Node-1 ←→ Node-2
          ↕   ⤬ ⤭   ↕
        Node-3 ←→ Node-4
          ↕   ⤬ ⤭   ↕
        Node-5 ←→ Node-6
```

## 3. Gossip Protocol

### 3.1 Membership Gossip

**Protocol**: SWIM (Scalable Weakly-consistent Infection-style Membership) inspired

**Parameters**:
- Gossip interval: 2 seconds
- Fanout (targets per round): 3 nodes
- Failure detection timeout: 10 seconds
- Suspected state duration: 6 seconds

**Node States**:
- `Alive`: Node is healthy and responding
- `Suspected`: Node failed to respond, awaiting confirmation
- `Dead`: Node confirmed as failed, removing from membership
- `Left`: Node gracefully departed from cluster

**Gossip Message Structure**:
```protobuf
message MembershipUpdate {
  repeated NodeState nodes = 1;
  uint64 incarnation_number = 2;
}

message NodeState {
  string node_id = 1;
  string address = 2;
  NodeStatus status = 3;
  uint64 incarnation = 4;
  uint64 timestamp = 5;
}

enum NodeStatus {
  ALIVE = 0;
  SUSPECTED = 1;
  DEAD = 2;
  LEFT = 3;
}
```

**Algorithm**:
```
Every gossip_interval:
  1. Select fanout random nodes from membership view
  2. For each target:
     a. Send current membership state
     b. Include incremental updates since last sync
     c. Piggyback CRDT delta updates (see 3.2)
  3. Merge received membership updates
  4. Update local membership view
  5. Trigger failure detection for suspected nodes
```

### 3.2 CRDT Delta Gossip

**Purpose**: Propagate data updates efficiently across cluster

**Parameters**:
- Piggyback on membership gossip
- Delta buffer size: Last 100 operations per CRDT
- Delta compression: Enabled for large payloads
- Anti-entropy full sync: Every 60 seconds

**Delta Message Structure**:
```protobuf
message CRDTDelta {
  string crdt_id = 1;
  CRDTType type = 2;
  bytes delta_payload = 3;
  VectorClock version = 4;
  uint64 timestamp = 5;
}

message VectorClock {
  map<string, uint64> clock = 1;
}

enum CRDTType {
  G_COUNTER = 0;
  PN_COUNTER = 1;
  LWW_REGISTER = 2;
  MV_REGISTER = 3;
}
```

**Algorithm**:
```
On local CRDT update:
  1. Generate delta representing the change
  2. Update local version vector
  3. Add delta to outgoing buffer
  4. Persist delta to append-only log

On gossip round:
  1. Select deltas not yet acknowledged by target
  2. Attach deltas to membership gossip message
  3. Track which nodes received which deltas

On receiving deltas:
  1. Verify delta causality using version vectors
  2. Apply delta to local CRDT state
  3. Resolve conflicts per CRDT semantics
  4. Update local version vector
  5. Acknowledge receipt to sender
```

### 3.3 Anti-Entropy

**Purpose**: Ensure all nodes eventually converge even with message loss

**Trigger Conditions**:
- Periodic: Every 60 seconds
- On-demand: When version vector divergence detected
- On recovery: After node restart or join

**Algorithm**:
```
Anti-entropy with peer:
  1. Exchange version vectors (Merkle tree roots)
  2. Identify divergent CRDT keys
  3. For each divergent key:
     a. Compare version vectors
     b. Request missing deltas or full state
     c. Apply received updates
  4. Update local version vector
```

**Merkle Tree Structure**:
- Root: Hash of all CRDT states
- Internal nodes: Hash of subtree
- Leaves: Hash of individual CRDT state
- Tree rebuilt on each anti-entropy round

## 4. CRDT Data Types

### 4.1 G-Counter (Grow-only Counter)

**Use Case**: Monotonically increasing counters (views, likes)

**State Representation**:
```go
type GCounter struct {
    NodeID string
    Counts map[string]uint64  // node_id -> count
}
```

**Operations**:
- `Increment(nodeID, delta)`: Increase counter for this node
- `Query()`: Return sum of all node counts
- `Merge(other)`: Take maximum per node

**Delta Generation**:
```go
type GCounterDelta struct {
    NodeID string
    Delta  uint64
}
```

### 4.2 PN-Counter (Positive-Negative Counter)

**Use Case**: Counters that can increase and decrease (inventory, balance)

**State Representation**:
```go
type PNCounter struct {
    NodeID string
    Increments map[string]uint64  // node_id -> positive increments
    Decrements map[string]uint64  // node_id -> negative decrements
}
```

**Operations**:
- `Increment(nodeID, delta)`: Increase positive counter
- `Decrement(nodeID, delta)`: Increase negative counter
- `Query()`: Return (sum of increments) - (sum of decrements)
- `Merge(other)`: Take maximum per node for both maps

### 4.3 LWW-Register (Last-Write-Wins Register)

**Use Case**: Single-value storage where latest timestamp wins

**State Representation**:
```go
type LWWRegister struct {
    Value     interface{}
    Timestamp uint64
    NodeID    string  // tie-breaker
}
```

**Operations**:
- `Set(value, timestamp, nodeID)`: Update if timestamp newer
- `Get()`: Return current value
- `Merge(other)`: Keep value with higher timestamp (nodeID for ties)

**Delta Generation**:
```go
type LWWRegisterDelta struct {
    Value     interface{}
    Timestamp uint64
    NodeID    string
}
```

### 4.4 MV-Register (Multi-Value Register)

**Use Case**: Preserve concurrent writes for application-level conflict resolution

**State Representation**:
```go
type MVRegister struct {
    Values    map[VectorClock]interface{}  // concurrent versions
}
```

**Operations**:
- `Set(value, vectorClock)`: Add new version, prune dominated
- `Get()`: Return all concurrent values
- `Merge(other)`: Union of non-dominated versions

**Conflict Resolution**: Application chooses strategy (latest, merge, prompt user)

## 5. Communication Layer

### 5.1 gRPC Service Definitions

```protobuf
// Inter-node communication
service NodeService {
  // Gossip membership and CRDT deltas
  rpc Gossip(GossipMessage) returns (GossipAck);

  // Anti-entropy synchronization
  rpc SyncState(SyncRequest) returns (stream SyncResponse);

  // Direct delta push (optional fast path)
  rpc PushDelta(CRDTDelta) returns (DeltaAck);

  // Health check
  rpc Ping(PingRequest) returns (PingResponse);
}

// Client API
service ClientService {
  // CRDT operations
  rpc IncrementCounter(IncrementRequest) returns (IncrementResponse);
  rpc DecrementCounter(DecrementRequest) returns (DecrementResponse);
  rpc GetCounter(GetRequest) returns (CounterResponse);

  rpc SetRegister(SetRequest) returns (SetResponse);
  rpc GetRegister(GetRequest) returns (RegisterResponse);

  // Cluster information
  rpc GetClusterStatus(StatusRequest) returns (StatusResponse);
}
```

### 5.2 Mutual TLS Configuration

**Certificate Hierarchy**:
```
Root CA
  ├── Intermediate CA (Node Certificates)
  │   ├── Node-1 Certificate
  │   ├── Node-2 Certificate
  │   └── Node-N Certificate
  └── Intermediate CA (Client Certificates)
      ├── Client-1 Certificate
      └── Client-M Certificate
```

**TLS Requirements**:
- TLS 1.3 minimum
- Certificate rotation: Every 90 days
- Mutual authentication required for all connections
- Node identity verified via certificate CN/SAN

**Configuration**:
```go
tlsConfig := &tls.Config{
    Certificates: []tls.Certificate{nodeCert},
    ClientCAs:    caCertPool,
    ClientAuth:   tls.RequireAndVerifyClientCert,
    MinVersion:   tls.VersionTLS13,
    CipherSuites: []uint16{
        tls.TLS_AES_256_GCM_SHA384,
        tls.TLS_CHACHA20_POLY1305_SHA256,
    },
}
```

## 6. Persistence Layer

### 6.1 Append-Only Log (WAL)

**Purpose**: Durability and crash recovery

**Format**: Sequential binary records

**Entry Structure**:
```protobuf
message LogEntry {
  uint64 sequence_number = 1;
  uint64 timestamp = 2;
  LogEntryType type = 3;
  bytes payload = 4;
  bytes checksum = 5;  // CRC32
}

enum LogEntryType {
  CRDT_DELTA = 0;
  SNAPSHOT_MARKER = 1;
  MEMBERSHIP_CHANGE = 2;
}
```

**Operations**:
- `Append(entry)`: Add entry to log, fsync
- `Read(start_sequence)`: Iterator from sequence number
- `Truncate(before_sequence)`: Remove old entries after snapshot

**Log Rotation**:
- New log file every 1GB or 24 hours
- Old logs deleted after successful snapshot
- Configurable retention period

### 6.2 Snapshot System

**Format**: Protocol Buffers

**Structure**:
```protobuf
message Snapshot {
  uint64 sequence_number = 1;  // Last log entry included
  uint64 timestamp = 2;
  map<string, CRDTState> crdts = 3;
  VectorClock global_version = 4;
  MembershipState membership = 5;
  SnapshotMetadata metadata = 6;
}

message CRDTState {
  string crdt_id = 1;
  CRDTType type = 2;
  bytes serialized_state = 3;
  VectorClock version = 4;
}
```

**Snapshot Policy**:
- Periodic: Every 5 minutes
- Size-based: When log exceeds 500MB
- On-demand: Manual trigger via admin API
- Atomic: Write to temp file, then rename

**Recovery Process**:
```
On node start:
  1. Load latest snapshot
  2. Restore CRDT states and membership
  3. Replay log entries after snapshot
  4. Initialize gossip with recovered membership
  5. Trigger anti-entropy with cluster
  6. Mark node as Alive in membership
```

## 7. Replication Strategy

### 7.1 Phase 1: Full Replication (N-way)

**Characteristics**:
- Every node stores complete dataset
- No data partitioning
- Optimal read availability
- Write cost scales with cluster size

**Suitable For**:
- Small to medium datasets (< 10GB)
- High read throughput requirements
- Prototype and initial deployment

### 7.2 Phase 2: K-way Replication (Future)

**Design Considerations**:

**Consistent Hashing**:
- Virtual nodes for load balancing
- Replication factor K (configurable)
- Preference list per key

**Data Placement**:
```go
type PartitionRing struct {
    VirtualNodes int         // e.g., 256 per physical node
    ReplicationFactor int    // K replicas
    HashFunction crypto.Hash // SHA-256
}

func (r *PartitionRing) GetReplicas(key string) []NodeID {
    hash := r.HashFunction(key)
    position := hash % r.RingSize
    return r.GetSuccessors(position, r.ReplicationFactor)
}
```

**Migration Path**:
1. Implement consistent hashing ring
2. Add partition metadata to gossip
3. Implement data migration protocol
4. Support hybrid mode (old + new scheme)
5. Gradual rollout with configurable K

## 8. Consistency Guarantees

### 8.1 Strong Eventual Consistency

**Definition**: All nodes that have received the same set of updates will have identical state

**Guarantees**:
- **Eventual Delivery**: Every update is eventually received by all nodes
- **Convergence**: Nodes with same updates converge to same state
- **Commutativity**: Update order doesn't affect final state (CRDT property)

**Achieved Through**:
- CRDT mathematical properties
- Gossip-based reliable broadcast
- Anti-entropy for completeness
- Version vectors for causality

### 8.2 Causal Consistency

**Version Vectors**: Track causality between operations

**Properties**:
- Writes that are causally related are seen in order
- Concurrent writes may be observed in any order
- Conflicts resolved deterministically by CRDT

**Example**:
```
Node A: Write X=1 (VA={A:1})
Node A: Write X=2 (VA={A:2})  // Causally after X=1
Node B: Write X=3 (VB={B:1})  // Concurrent with X=2

All nodes will see X=1 before X=2, but X=3 order is non-deterministic
Final state determined by CRDT conflict resolution
```

## 9. Failure Handling

### 9.1 Node Failures

**Detection**:
- Failed ping responses (3 consecutive)
- Gossip timeout (10 seconds)
- Suspected state transition
- Confirmation from multiple nodes

**Recovery**:
```
On node failure detection:
  1. Mark node as Suspected
  2. Continue processing (no blocking)
  3. After timeout, mark as Dead
  4. Remove from gossip targets
  5. Keep in membership for 24h (join detection)

On node recovery:
  1. Load snapshot and replay log
  2. Join cluster via seed nodes
  3. Receive membership via gossip
  4. Trigger anti-entropy with all nodes
  5. Mark self as Alive
```

### 9.2 Network Partitions

**Behavior**:
- Each partition continues operating independently
- Updates within partition propagate normally
- No blocking or unavailability

**Partition Healing**:
```
When partition heals:
  1. Gossip reconnects partitions
  2. Membership information merges
  3. Version vectors reveal divergence
  4. Anti-entropy synchronizes state
  5. CRDTs resolve conflicts automatically
  6. Convergence achieved
```

**Example Scenario**:
```
Initial: [Node-1, Node-2, Node-3]
Partition: [Node-1, Node-2] | [Node-3]

During partition:
  - Left: Increment counter X
  - Right: Increment counter X
  - Both partitions accept writes

After healing:
  - Gossip exchanges deltas
  - G-Counter merges increments
  - Final value = sum of both partitions
```

### 9.3 Data Corruption

**Detection**:
- CRC32 checksums in log entries
- Snapshot integrity validation
- Protocol buffer parsing errors

**Recovery**:
```
On corruption detection:
  1. Log corruption event
  2. Attempt previous snapshot
  3. If all snapshots corrupted:
     a. Clear local state
     b. Rejoin as new node
     c. Sync from cluster via anti-entropy
  4. Alert monitoring system
```

## 10. Monitoring and Observability

### 10.1 Metrics

**Node Metrics**:
- Gossip messages sent/received
- CRDT operations per second
- Anti-entropy sync duration
- Log append latency
- Snapshot creation time
- Memory usage per CRDT type

**Cluster Metrics**:
- Membership size and changes
- Network partition events
- Version vector divergence
- Inter-node latency percentiles

**Client Metrics**:
- Request latency (p50, p95, p99)
- Throughput (ops/sec)
- Error rates by type

### 10.2 Health Checks

**Node Health**:
```
/health endpoint returns:
{
  "status": "healthy|degraded|unhealthy",
  "cluster_size": 6,
  "reachable_nodes": 5,
  "log_sequence": 12345,
  "last_snapshot": "2025-11-03T10:30:00Z",
  "crdts_count": 1500,
  "memory_usage_mb": 245
}
```

## 11. Configuration

### 11.1 Node Configuration

```yaml
node:
  id: "node-1"  # Unique identifier
  bind_address: "0.0.0.0:8080"
  advertise_address: "192.168.1.10:8080"
  data_dir: "/var/lib/inmemorydb"

gossip:
  interval_ms: 2000
  fanout: 3
  failure_timeout_ms: 10000
  suspected_timeout_ms: 6000

persistence:
  log_max_size_mb: 1024
  snapshot_interval_sec: 300
  log_retention_hours: 168  # 7 days

replication:
  mode: "full"  # or "k-way"
  replication_factor: 3  # for k-way

security:
  tls_enabled: true
  cert_file: "/etc/inmemorydb/certs/node.crt"
  key_file: "/etc/inmemorydb/certs/node.key"
  ca_file: "/etc/inmemorydb/certs/ca.crt"

client_api:
  enabled: true
  bind_address: "0.0.0.0:9090"
  max_concurrent_requests: 1000
```

## 12. Security Considerations

### 12.1 Threat Model

**In Scope**:
- Network eavesdropping (mitigated by TLS)
- Unauthorized node joining (mitigated by mTLS)
- Man-in-the-middle attacks (mitigated by certificate verification)
- Data tampering (mitigated by checksums)

**Out of Scope** (Future Work):
- Byzantine fault tolerance
- Denial of service attacks
- Resource exhaustion attacks
- Authentication/authorization for client operations

### 12.2 Security Best Practices

1. **Certificate Management**:
   - Use hardware security modules (HSM) for CA keys
   - Automate certificate rotation
   - Monitor certificate expiration

2. **Network Security**:
   - Deploy in private network (VPC)
   - Use firewall rules to restrict access
   - Enable audit logging

3. **Data Protection**:
   - Encrypt data at rest (future feature)
   - Secure deletion of log files
   - Memory scrubbing for sensitive data

## 13. Performance Considerations

### 13.1 Expected Performance

**Throughput** (per node, 4-core, 16GB RAM):
- Counter operations: ~50,000 ops/sec
- Register operations: ~30,000 ops/sec
- Gossip overhead: ~2-5% CPU

**Latency**:
- Local read: < 1ms
- Local write: < 5ms (including log sync)
- Cluster propagation: 2-6 seconds (2 * gossip_interval)

**Scalability**:
- Cluster size: 10-100 nodes (full replication)
- Data size: Up to 10GB per node
- Network bandwidth: ~10-50 Mbps per node

### 13.2 Optimization Strategies

1. **Delta Compression**: Use Snappy compression for large deltas
2. **Batching**: Group multiple CRDT updates in single gossip message
3. **Connection Pooling**: Reuse gRPC connections
4. **Memory Management**: Use sync.Pool for temporary objects
5. **Concurrent Processing**: Parallelize CRDT merges

## 14. Testing Strategy

### 14.1 Unit Tests

- Individual CRDT type correctness
- Version vector operations
- Log append/read operations
- Gossip message serialization

### 14.2 Integration Tests

- Multi-node gossip propagation
- Anti-entropy convergence
- Snapshot and recovery
- gRPC communication

### 14.3 Chaos Testing

- Random node failures
- Network partitions (use toxiproxy)
- Packet loss and delays
- Concurrent updates from multiple nodes

### 14.4 Property-Based Tests

- CRDT convergence properties
- Commutativity and associativity
- Idempotence of merges

## 15. Future Enhancements

### 15.1 Phase 2 Features

1. **K-way Replication**: Reduce memory footprint
2. **Read Repair**: Opportunistic anti-entropy
3. **Tunable Consistency**: Optional read-your-writes
4. **More CRDT Types**: Sets, Maps, Lists
5. **Compression**: Snappy/LZ4 for snapshots

### 15.2 Phase 3 Features

1. **Multi-datacenter Support**: WAN-aware gossip
2. **Query Language**: Simple query DSL
3. **Secondary Indexes**: Efficient lookups
4. **Observability**: OpenTelemetry integration
5. **Admin UI**: Web-based cluster management

## 16. References

1. **CRDTs**: Shapiro et al., "Conflict-free Replicated Data Types"
2. **SWIM**: Das et al., "SWIM: Scalable Weakly-consistent Infection-style Process Group Membership Protocol"
3. **Gossip**: van Renesse et al., "Efficient Reconciliation and Flow Control for Anti-Entropy Protocols"
4. **Riak**: Basho, "Riak Core" (inspiration for anti-entropy)
5. **Cassandra**: Apache Cassandra gossip implementation

## 17. Glossary

- **CRDT**: Conflict-free Replicated Data Type
- **Delta**: Incremental state change for a CRDT
- **Gossip**: Epidemic-style communication protocol
- **Anti-entropy**: Periodic full state reconciliation
- **Version Vector**: Causality tracking mechanism
- **Fanout**: Number of gossip targets per round
- **Incarnation**: Counter for node identity conflicts
- **WAL**: Write-Ahead Log
- **mTLS**: Mutual TLS authentication

---

**Document Version**: 0.1
**Last Updated**: 2025-11-03
**Status**: Draft