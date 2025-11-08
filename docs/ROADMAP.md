# Development Roadmap: Distributed In-Memory Database

## Overview

This document provides a detailed implementation plan for building the distributed in-memory database. The development is organized into phases, each with specific deliverables, testing requirements, and success criteria. The plan follows a bottom-up approach, building foundational components first and progressively adding complexity.

## Development Principles

1. **Test-Driven Development**: Write tests before implementation
2. **Incremental Delivery**: Each phase produces a working system
3. **Validation at Each Step**: Verify correctness before moving forward
4. **Documentation**: Update docs as implementation progresses
5. **Performance Baseline**: Measure and track performance metrics

## Phase 0: Project Setup and Foundation (Week 1)

### Goal
Establish project structure, tooling, and basic infrastructure.

### Tasks

#### 0.1 Project Structure
```
in-memorydb/
├── cmd/
│   ├── node/              # Node server executable
│   └── client/            # CLI client tool
├── pkg/
│   ├── crdt/              # CRDT implementations
│   ├── gossip/            # Gossip protocol
│   ├── membership/        # Membership management
│   ├── storage/           # Storage engine
│   ├── persistence/       # Log and snapshot
│   ├── grpc/              # gRPC services
│   ├── config/            # Configuration
│   ├── crypto/            # TLS/certificate handling
│   └── util/              # Utilities and helpers
├── api/
│   └── proto/             # Protocol buffer definitions
├── test/
│   ├── integration/       # Integration tests
│   └── chaos/             # Chaos/fault injection tests
├── scripts/
│   ├── gen-certs.sh       # Certificate generation
│   └── run-cluster.sh     # Local cluster launcher
├── configs/
│   └── node-template.yaml # Configuration template
├── docs/                  # Additional documentation
├── go.mod
├── go.sum
├── Makefile
├── RFC.md
├── ROADMAP.md
└── CLAUDE.md
```

**Implementation**:
```bash
# Create directory structure
mkdir -p cmd/{node,client} pkg/{crdt,gossip,membership,storage,persistence,grpc,config,crypto,util}
mkdir -p api/proto test/{integration,chaos} scripts configs docs

# Initialize additional modules if needed
```

#### 0.2 Dependencies and Tools

**Add to go.mod**:
```bash
go get google.golang.org/grpc@latest
go get google.golang.org/protobuf/cmd/protoc-gen-go@latest
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go get github.com/stretchr/testify@latest
go get gopkg.in/yaml.v3@latest
go get github.com/google/uuid@latest
go get github.com/hashicorp/memberlist@latest  # Reference for SWIM
```

**Install Development Tools**:
```bash
# Protocol Buffers compiler
brew install protobuf  # macOS
# or download from: https://github.com/protocolbuffers/protobuf/releases

# Install Go protoc plugins
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Optional: Testing and linting tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

#### 0.3 Makefile

Create `Makefile`:
```makefile
.PHONY: all build test proto clean

all: proto build test

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
	       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	       api/proto/*.proto

build:
	go build -o bin/node cmd/node/main.go
	go build -o bin/client cmd/client/main.go

test:
	go test -v -race ./...

integration-test:
	go test -v -tags=integration ./test/integration/...

bench:
	go test -bench=. -benchmem ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/
	rm -f api/proto/*.pb.go

run-node:
	./bin/node --config=configs/node1.yaml

cluster-local:
	./scripts/run-cluster.sh 3
```

#### 0.4 Configuration System

**Create `pkg/config/config.go`**:
```go
package config

import (
    "os"
    "gopkg.in/yaml.v3"
)

type Config struct {
    Node        NodeConfig        `yaml:"node"`
    Gossip      GossipConfig      `yaml:"gossip"`
    Persistence PersistenceConfig `yaml:"persistence"`
    Replication ReplicationConfig `yaml:"replication"`
    Security    SecurityConfig    `yaml:"security"`
    ClientAPI   ClientAPIConfig   `yaml:"client_api"`
}

type NodeConfig struct {
    ID               string `yaml:"id"`
    BindAddress      string `yaml:"bind_address"`
    AdvertiseAddress string `yaml:"advertise_address"`
    DataDir          string `yaml:"data_dir"`
}

// ... other config structs from RFC section 11.1

func LoadConfig(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}
```

**Create template**: `configs/node-template.yaml`

#### 0.5 Logging and Observability Setup

**Create `pkg/util/logger.go`**:
```go
package util

import (
    "log"
    "os"
)

type Logger struct {
    nodeID string
    logger *log.Logger
}

func NewLogger(nodeID string) *Logger {
    return &Logger{
        nodeID: nodeID,
        logger: log.New(os.Stdout, "["+nodeID+"] ", log.LstdFlags),
    }
}

// Info, Error, Debug methods...
```

### Testing
- [ ] Verify all directories created
- [ ] Test configuration loading with sample YAML
- [ ] Verify all dependencies install correctly
- [ ] Run `make proto` (even with empty proto files)
- [ ] Verify Makefile targets work

### Success Criteria
- Clean `go build` of empty main.go files
- Configuration loads from YAML
- Project structure matches plan
- Development tools installed and working

---

## Phase 1: CRDT Core Implementation (Week 2-3)

### Goal
Implement and thoroughly test the four core CRDT types with their merge and delta generation logic.

### Tasks

#### 1.1 CRDT Interface and Common Types

**Create `pkg/crdt/types.go`**:
```go
package crdt

import "time"

type VectorClock map[string]uint64

func (vc VectorClock) Copy() VectorClock {
    // Deep copy implementation
}

func (vc VectorClock) Merge(other VectorClock) VectorClock {
    // Element-wise maximum
}

func (vc VectorClock) Compare(other VectorClock) Ordering {
    // Returns: Concurrent, Before, After, Equal
}

type Ordering int
const (
    Concurrent Ordering = iota
    Before
    After
    Equal
)

// Base interface for all CRDTs
type CRDT interface {
    // Merge full state from another replica
    Merge(other CRDT) error

    // Apply a delta update
    ApplyDelta(delta Delta) error

    // Get current state version
    Version() VectorClock

    // Serialize state to bytes
    Marshal() ([]byte, error)

    // Deserialize state from bytes
    Unmarshal(data []byte) error
}

type Delta interface {
    GetVersion() VectorClock
    Marshal() ([]byte, error)
}
```

**Create `pkg/crdt/vector_clock.go`** with full implementation.

**Tests**: `pkg/crdt/vector_clock_test.go`
- Test merge operations
- Test comparison logic (concurrent, before, after)
- Test copy operations
- Property-based tests for commutativity

#### 1.2 G-Counter Implementation

**Create `pkg/crdt/gcounter.go`**:
```go
package crdt

type GCounter struct {
    nodeID  string
    counts  map[string]uint64
    version VectorClock
}

func NewGCounter(nodeID string) *GCounter {
    return &GCounter{
        nodeID:  nodeID,
        counts:  make(map[string]uint64),
        version: make(VectorClock),
    }
}

func (g *GCounter) Increment(delta uint64) *GCounterDelta {
    g.counts[g.nodeID] += delta
    g.version[g.nodeID]++

    return &GCounterDelta{
        NodeID:  g.nodeID,
        Delta:   delta,
        Version: g.version.Copy(),
    }
}

func (g *GCounter) Value() uint64 {
    var sum uint64
    for _, count := range g.counts {
        sum += count
    }
    return sum
}

func (g *GCounter) Merge(other *GCounter) {
    for nodeID, count := range other.counts {
        if count > g.counts[nodeID] {
            g.counts[nodeID] = count
        }
    }
    g.version = g.version.Merge(other.version)
}

func (g *GCounter) ApplyDelta(delta *GCounterDelta) error {
    // Validate causality
    if g.version.Compare(delta.Version) == After {
        return nil // Already applied
    }

    g.counts[delta.NodeID] += delta.Delta
    g.version = g.version.Merge(delta.Version)
    return nil
}

type GCounterDelta struct {
    NodeID  string
    Delta   uint64
    Version VectorClock
}
```

**Tests**: `pkg/crdt/gcounter_test.go`
```go
func TestGCounterBasicIncrement(t *testing.T) {
    counter := NewGCounter("node1")
    counter.Increment(5)
    assert.Equal(t, uint64(5), counter.Value())
}

func TestGCounterMerge(t *testing.T) {
    c1 := NewGCounter("node1")
    c2 := NewGCounter("node2")

    c1.Increment(5)
    c2.Increment(3)

    c1.Merge(c2)

    assert.Equal(t, uint64(8), c1.Value())
}

func TestGCounterConvergence(t *testing.T) {
    // Create 3 replicas, apply random increments, merge in different orders
    // Verify all replicas converge to same value
}

func TestGCounterIdempotence(t *testing.T) {
    // Apply same delta twice, verify no duplicate counting
}

// Property-based test
func TestGCounterCommutativity(t *testing.T) {
    // Generate random sequence of operations
    // Apply in different orders
    // Verify same final state
}
```

#### 1.3 PN-Counter Implementation

**Create `pkg/crdt/pncounter.go`** - Similar structure to G-Counter but with two G-Counters (positive and negative).

**Tests**: `pkg/crdt/pncounter_test.go`
- Test increment and decrement
- Test that Value() = Increments - Decrements
- Convergence tests
- Negative values handling

#### 1.4 LWW-Register Implementation

**Create `pkg/crdt/lww_register.go`**:
```go
package crdt

type LWWRegister struct {
    value     interface{}
    timestamp uint64
    nodeID    string
    version   VectorClock
}

func NewLWWRegister(nodeID string) *LWWRegister {
    return &LWWRegister{
        nodeID:  nodeID,
        version: make(VectorClock),
    }
}

func (r *LWWRegister) Set(value interface{}) *LWWRegisterDelta {
    r.timestamp = uint64(time.Now().UnixNano())
    r.value = value
    r.version[r.nodeID]++

    return &LWWRegisterDelta{
        Value:     value,
        Timestamp: r.timestamp,
        NodeID:    r.nodeID,
        Version:   r.version.Copy(),
    }
}

func (r *LWWRegister) Get() interface{} {
    return r.value
}

func (r *LWWRegister) Merge(other *LWWRegister) {
    if other.timestamp > r.timestamp ||
       (other.timestamp == r.timestamp && other.nodeID > r.nodeID) {
        r.value = other.value
        r.timestamp = other.timestamp
        r.nodeID = other.nodeID
    }
    r.version = r.version.Merge(other.version)
}

// ApplyDelta implementation...
```

**Tests**: `pkg/crdt/lww_register_test.go`
- Test last-write-wins behavior
- Test timestamp ordering
- Test tie-breaking with nodeID
- Concurrent writes convergence

#### 1.5 MV-Register Implementation

**Create `pkg/crdt/mv_register.go`**:
```go
package crdt

type MVRegister struct {
    nodeID string
    values map[string]ValueVersion  // vectorClock hash -> value
}

type ValueVersion struct {
    Value   interface{}
    Version VectorClock
}

func (r *MVRegister) Set(value interface{}) *MVRegisterDelta {
    // Create new version, remove dominated versions
}

func (r *MVRegister) Get() []interface{} {
    // Return all concurrent values
}

func (r *MVRegister) Merge(other *MVRegister) {
    // Union non-dominated versions
}
```

**Tests**: `pkg/crdt/mv_register_test.go`
- Test concurrent writes preserved
- Test dominated version removal
- Test convergence with concurrent updates

### Testing Strategy

**Unit Tests**:
```bash
# Run all CRDT tests
go test -v ./pkg/crdt/...

# Run with race detector
go test -race ./pkg/crdt/...

# Run benchmarks
go test -bench=. ./pkg/crdt/...
```

**Benchmark Tests**: `pkg/crdt/benchmark_test.go`
```go
func BenchmarkGCounterIncrement(b *testing.B) {
    counter := NewGCounter("node1")
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        counter.Increment(1)
    }
}

func BenchmarkGCounterMerge(b *testing.B) {
    // Benchmark merge operations
}
```

**Property-Based Tests** (using rapid or gopter):
```go
func TestCRDTProperties(t *testing.T) {
    properties := []struct{
        name string
        test func(t *rapid.T)
    }{
        {"Commutativity", testCommutativity},
        {"Associativity", testAssociativity},
        {"Idempotence", testIdempotence},
    }

    for _, prop := range properties {
        t.Run(prop.name, rapid.MakeCheck(prop.test))
    }
}
```

### Success Criteria
- [ ] All 4 CRDT types implemented
- [ ] 100% test coverage for CRDT operations
- [ ] All property-based tests pass (1000+ iterations)
- [ ] Benchmarks show acceptable performance (>10k ops/sec)
- [ ] Race detector shows no issues
- [ ] Documentation complete for each CRDT type

---

## Phase 2: Storage Engine (Week 4)

### Goal
Build the in-memory storage layer that manages multiple CRDT instances.

### Tasks

#### 2.1 Storage Engine Interface

**Create `pkg/storage/engine.go`**:
```go
package storage

import (
    "in-memorydb/pkg/crdt"
    "sync"
)

type Engine struct {
    nodeID string
    mu     sync.RWMutex
    crdts  map[string]CRDTEntry
}

type CRDTEntry struct {
    Type    crdt.CRDTType
    CRDT    crdt.CRDT
    Version crdt.VectorClock
}

type CRDTType int
const (
    TypeGCounter CRDTType = iota
    TypePNCounter
    TypeLWWRegister
    TypeMVRegister
)

func NewEngine(nodeID string) *Engine {
    return &Engine{
        nodeID: nodeID,
        crdts:  make(map[string]CRDTEntry),
    }
}

// Create or get CRDT by ID
func (e *Engine) GetOrCreate(id string, crdtType CRDTType) (crdt.CRDT, error) {
    e.mu.Lock()
    defer e.mu.Unlock()

    if entry, exists := e.crdts[id]; exists {
        if entry.Type != crdtType {
            return nil, ErrTypeMismatch
        }
        return entry.CRDT, nil
    }

    // Create new CRDT based on type
    newCRDT := e.createCRDT(crdtType)
    e.crdts[id] = CRDTEntry{
        Type: crdtType,
        CRDT: newCRDT,
        Version: make(crdt.VectorClock),
    }

    return newCRDT, nil
}

func (e *Engine) ApplyDelta(id string, delta crdt.Delta) error {
    e.mu.Lock()
    defer e.mu.Unlock()

    entry, exists := e.crdts[id]
    if !exists {
        return ErrCRDTNotFound
    }

    return entry.CRDT.ApplyDelta(delta)
}

func (e *Engine) GetVersion(id string) (crdt.VectorClock, error) {
    e.mu.RLock()
    defer e.mu.RUnlock()

    entry, exists := e.crdts[id]
    if !exists {
        return nil, ErrCRDTNotFound
    }

    return entry.Version, nil
}

func (e *Engine) GetAllVersions() map[string]crdt.VectorClock {
    e.mu.RLock()
    defer e.mu.RUnlock()

    versions := make(map[string]crdt.VectorClock)
    for id, entry := range e.crdts {
        versions[id] = entry.Version.Copy()
    }
    return versions
}

// For anti-entropy
func (e *Engine) GetDivergentKeys(remoteVersions map[string]crdt.VectorClock) []string {
    e.mu.RLock()
    defer e.mu.RUnlock()

    var divergent []string

    // Check local keys
    for id, localVer := range e.crdts {
        remoteVer, exists := remoteVersions[id]
        if !exists || localVer.Version.Compare(remoteVer) == crdt.Concurrent {
            divergent = append(divergent, id)
        }
    }

    // Check for keys only in remote
    for id := range remoteVersions {
        if _, exists := e.crdts[id]; !exists {
            divergent = append(divergent, id)
        }
    }

    return divergent
}

func (e *Engine) Snapshot() (map[string][]byte, error) {
    e.mu.RLock()
    defer e.mu.RUnlock()

    snapshot := make(map[string][]byte)
    for id, entry := range e.crdts {
        data, err := entry.CRDT.Marshal()
        if err != nil {
            return nil, err
        }
        snapshot[id] = data
    }

    return snapshot, nil
}
```

#### 2.2 CRDT Factory

**Create `pkg/storage/factory.go`**:
```go
func (e *Engine) createCRDT(crdtType CRDTType) crdt.CRDT {
    switch crdtType {
    case TypeGCounter:
        return crdt.NewGCounter(e.nodeID)
    case TypePNCounter:
        return crdt.NewPNCounter(e.nodeID)
    case TypeLWWRegister:
        return crdt.NewLWWRegister(e.nodeID)
    case TypeMVRegister:
        return crdt.NewMVRegister(e.nodeID)
    default:
        panic("unknown CRDT type")
    }
}
```

#### 2.3 Delta Buffer

**Create `pkg/storage/delta_buffer.go`**:
```go
package storage

type DeltaBuffer struct {
    mu      sync.RWMutex
    deltas  []DeltaEntry
    maxSize int
}

type DeltaEntry struct {
    CRDTID    string
    Delta     crdt.Delta
    Timestamp uint64
    Acked     map[string]bool  // which nodes acknowledged
}

func NewDeltaBuffer(maxSize int) *DeltaBuffer {
    return &DeltaBuffer{
        deltas:  make([]DeltaEntry, 0, maxSize),
        maxSize: maxSize,
    }
}

func (b *DeltaBuffer) Add(crdtID string, delta crdt.Delta) {
    b.mu.Lock()
    defer b.mu.Unlock()

    entry := DeltaEntry{
        CRDTID:    crdtID,
        Delta:     delta,
        Timestamp: uint64(time.Now().Unix()),
        Acked:     make(map[string]bool),
    }

    b.deltas = append(b.deltas, entry)

    // Trim if exceeded max size
    if len(b.deltas) > b.maxSize {
        b.deltas = b.deltas[len(b.deltas)-b.maxSize:]
    }
}

func (b *DeltaBuffer) GetUnacked(nodeID string) []DeltaEntry {
    b.mu.RLock()
    defer b.mu.RUnlock()

    var unacked []DeltaEntry
    for _, entry := range b.deltas {
        if !entry.Acked[nodeID] {
            unacked = append(unacked, entry)
        }
    }
    return unacked
}

func (b *DeltaBuffer) MarkAcked(nodeID string, timestamp uint64) {
    b.mu.Lock()
    defer b.mu.Unlock()

    for i := range b.deltas {
        if b.deltas[i].Timestamp <= timestamp {
            b.deltas[i].Acked[nodeID] = true
        }
    }
}
```

### Testing

**Create `pkg/storage/engine_test.go`**:
```go
func TestEngineGetOrCreate(t *testing.T) {
    engine := NewEngine("node1")

    counter, err := engine.GetOrCreate("counter1", TypeGCounter)
    assert.NoError(t, err)
    assert.NotNil(t, counter)

    // Getting again should return same instance
    counter2, err := engine.GetOrCreate("counter1", TypeGCounter)
    assert.NoError(t, err)
    assert.Equal(t, counter, counter2)
}

func TestEngineApplyDelta(t *testing.T) {
    engine := NewEngine("node1")
    counter, _ := engine.GetOrCreate("counter1", TypeGCounter)

    gCounter := counter.(*crdt.GCounter)
    delta := gCounter.Increment(5)

    // Apply to another engine
    engine2 := NewEngine("node2")
    counter2, _ := engine2.GetOrCreate("counter1", TypeGCounter)

    err := engine2.ApplyDelta("counter1", delta)
    assert.NoError(t, err)

    gCounter2 := counter2.(*crdt.GCounter)
    assert.Equal(t, uint64(5), gCounter2.Value())
}

func TestEngineConcurrentAccess(t *testing.T) {
    engine := NewEngine("node1")

    // Launch multiple goroutines accessing same CRDTs
    // Verify no race conditions
}

func TestEngineDeltaBuffer(t *testing.T) {
    buffer := NewDeltaBuffer(100)

    // Add deltas
    // Test GetUnacked
    // Test MarkAcked
}
```

**Concurrency Tests**:
```go
func TestStorageConcurrency(t *testing.T) {
    engine := NewEngine("node1")

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            crdtID := fmt.Sprintf("counter%d", id%10)
            counter, _ := engine.GetOrCreate(crdtID, TypeGCounter)
            gc := counter.(*crdt.GCounter)

            for j := 0; j < 100; j++ {
                gc.Increment(1)
            }
        }(i)
    }

    wg.Wait()

    // Verify all increments accounted for
}
```

### Success Criteria
- [ ] Storage engine manages multiple CRDT instances
- [ ] Thread-safe concurrent access
- [ ] Delta buffer tracks unacked deltas
- [ ] Version vector comparison works correctly
- [ ] Race detector passes
- [ ] Performance: >10k CRDT operations/sec

---

## Phase 3: Persistence Layer (Week 5)

### Goal
Implement append-only log and snapshot system for durability.

### Tasks

#### 3.1 Protocol Buffer Definitions

**Create `api/proto/persistence.proto`**:
```protobuf
syntax = "proto3";

package persistence;
option go_package = "in-memorydb/api/proto;proto";

message LogEntry {
  uint64 sequence_number = 1;
  uint64 timestamp = 2;
  LogEntryType type = 3;
  bytes payload = 4;
  bytes checksum = 5;
}

enum LogEntryType {
  CRDT_DELTA = 0;
  SNAPSHOT_MARKER = 1;
  MEMBERSHIP_CHANGE = 2;
}

message Snapshot {
  uint64 sequence_number = 1;
  uint64 timestamp = 2;
  map<string, CRDTState> crdts = 3;
  map<string, uint64> global_version = 4;
  bytes metadata = 5;
}

message CRDTState {
  string crdt_id = 1;
  int32 crdt_type = 2;
  bytes serialized_state = 3;
  map<string, uint64> version = 4;
}
```

**Generate**:
```bash
make proto
```

#### 3.2 Append-Only Log

**Create `pkg/persistence/log.go`**:
```go
package persistence

import (
    "bufio"
    "encoding/binary"
    "hash/crc32"
    "os"
    "sync"
)

type AppendLog struct {
    mu            sync.Mutex
    file          *os.File
    writer        *bufio.Writer
    sequenceNum   uint64
    currentSize   int64
    maxSize       int64
    rotateCallback func()
}

func NewAppendLog(path string, maxSize int64) (*AppendLog, error) {
    file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }

    // Read existing file to get last sequence number
    lastSeq, err := readLastSequence(path)
    if err != nil {
        lastSeq = 0
    }

    return &AppendLog{
        file:        file,
        writer:      bufio.NewWriter(file),
        sequenceNum: lastSeq,
        maxSize:     maxSize,
    }, nil
}

func (log *AppendLog) Append(entry *proto.LogEntry) error {
    log.mu.Lock()
    defer log.mu.Unlock()

    log.sequenceNum++
    entry.SequenceNumber = log.sequenceNum
    entry.Timestamp = uint64(time.Now().UnixNano())

    // Calculate checksum
    data, err := proto.Marshal(entry)
    if err != nil {
        return err
    }
    entry.Checksum = crc32.ChecksumIEEE(data)

    // Write length prefix
    length := uint32(len(data))
    if err := binary.Write(log.writer, binary.LittleEndian, length); err != nil {
        return err
    }

    // Write entry
    if _, err := log.writer.Write(data); err != nil {
        return err
    }

    // Flush to disk (fsync)
    if err := log.writer.Flush(); err != nil {
        return err
    }
    if err := log.file.Sync(); err != nil {
        return err
    }

    log.currentSize += int64(4 + length)

    // Check if rotation needed
    if log.maxSize > 0 && log.currentSize >= log.maxSize {
        if log.rotateCallback != nil {
            go log.rotateCallback()
        }
    }

    return nil
}

func (log *AppendLog) Read(startSeq uint64) (*LogReader, error) {
    return NewLogReader(log.file.Name(), startSeq)
}

type LogReader struct {
    file   *os.File
    reader *bufio.Reader
}

func NewLogReader(path string, startSeq uint64) (*LogReader, error) {
    file, err := os.Open(path)
    if err != nil {
        return nil, err
    }

    reader := &LogReader{
        file:   file,
        reader: bufio.NewReader(file),
    }

    // Skip to start sequence
    if err := reader.skipTo(startSeq); err != nil {
        return nil, err
    }

    return reader, nil
}

func (r *LogReader) Next() (*proto.LogEntry, error) {
    // Read length
    var length uint32
    if err := binary.Read(r.reader, binary.LittleEndian, &length); err != nil {
        if err == io.EOF {
            return nil, io.EOF
        }
        return nil, err
    }

    // Read entry data
    data := make([]byte, length)
    if _, err := io.ReadFull(r.reader, data); err != nil {
        return nil, err
    }

    // Unmarshal
    var entry proto.LogEntry
    if err := proto.Unmarshal(data, &entry); err != nil {
        return nil, err
    }

    // Verify checksum
    entry.Checksum = 0  // Clear for verification
    entryData, _ := proto.Marshal(&entry)
    expectedChecksum := crc32.ChecksumIEEE(entryData)
    if entry.Checksum != expectedChecksum {
        return nil, ErrCorruptedEntry
    }

    return &entry, nil
}

func (r *LogReader) Close() error {
    return r.file.Close()
}
```

#### 3.3 Snapshot Manager

**Create `pkg/persistence/snapshot.go`**:
```go
package persistence

import (
    "in-memorydb/api/proto"
    "in-memorydb/pkg/storage"
    "os"
    "path/filepath"
    "time"
)

type SnapshotManager struct {
    dataDir       string
    engine        *storage.Engine
    log           *AppendLog
    interval      time.Duration
    lastSnapshot  uint64
}

func NewSnapshotManager(dataDir string, engine *storage.Engine, log *AppendLog, interval time.Duration) *SnapshotManager {
    return &SnapshotManager{
        dataDir:  dataDir,
        engine:   engine,
        log:      log,
        interval: interval,
    }
}

func (sm *SnapshotManager) Start(ctx context.Context) {
    ticker := time.NewTicker(sm.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if err := sm.TakeSnapshot(); err != nil {
                log.Printf("Snapshot failed: %v", err)
            }
        case <-ctx.Done():
            return
        }
    }
}

func (sm *SnapshotManager) TakeSnapshot() error {
    // Get current log sequence
    currentSeq := sm.log.GetSequenceNumber()

    // Get snapshot from engine
    snapshotData, err := sm.engine.Snapshot()
    if err != nil {
        return err
    }

    // Build protobuf snapshot
    snapshot := &proto.Snapshot{
        SequenceNumber: currentSeq,
        Timestamp:      uint64(time.Now().Unix()),
        Crdts:          make(map[string]*proto.CRDTState),
        GlobalVersion:  sm.engine.GetGlobalVersion(),
    }

    for id, data := range snapshotData {
        entry := sm.engine.GetEntry(id)
        snapshot.Crdts[id] = &proto.CRDTState{
            CrdtId:          id,
            CrdtType:        int32(entry.Type),
            SerializedState: data,
            Version:         entry.Version,
        }
    }

    // Write to temp file
    tempPath := filepath.Join(sm.dataDir, "snapshot.tmp")
    file, err := os.Create(tempPath)
    if err != nil {
        return err
    }
    defer file.Close()

    // Marshal and write
    data, err := proto.Marshal(snapshot)
    if err != nil {
        return err
    }

    if _, err := file.Write(data); err != nil {
        return err
    }

    if err := file.Sync(); err != nil {
        return err
    }

    // Atomic rename
    snapshotPath := filepath.Join(sm.dataDir, fmt.Sprintf("snapshot-%d.pb", currentSeq))
    if err := os.Rename(tempPath, snapshotPath); err != nil {
        return err
    }

    sm.lastSnapshot = currentSeq

    // Clean up old snapshots (keep last 3)
    sm.cleanOldSnapshots()

    return nil
}

func (sm *SnapshotManager) LoadLatest() (*proto.Snapshot, error) {
    files, err := filepath.Glob(filepath.Join(sm.dataDir, "snapshot-*.pb"))
    if err != nil {
        return nil, err
    }

    if len(files) == 0 {
        return nil, ErrNoSnapshot
    }

    // Sort and get latest
    sort.Strings(files)
    latestFile := files[len(files)-1]

    data, err := os.ReadFile(latestFile)
    if err != nil {
        return nil, err
    }

    var snapshot proto.Snapshot
    if err := proto.Unmarshal(data, &snapshot); err != nil {
        return nil, err
    }

    return &snapshot, nil
}

func (sm *SnapshotManager) RestoreFromSnapshot(snapshot *proto.Snapshot) error {
    // Restore CRDTs to engine
    for id, state := range snapshot.Crdts {
        crdtType := storage.CRDTType(state.CrdtType)
        crdt, err := sm.engine.GetOrCreate(id, crdtType)
        if err != nil {
            return err
        }

        if err := crdt.Unmarshal(state.SerializedState); err != nil {
            return err
        }
    }

    sm.lastSnapshot = snapshot.SequenceNumber
    return nil
}
```

#### 3.4 Recovery Coordinator

**Create `pkg/persistence/recovery.go`**:
```go
package persistence

func RecoverNode(dataDir string, engine *storage.Engine) error {
    log.Printf("Starting node recovery from %s", dataDir)

    // Initialize persistence components
    appendLog, err := NewAppendLog(filepath.Join(dataDir, "log"), 1<<30) // 1GB
    if err != nil {
        return err
    }

    snapshotMgr := NewSnapshotManager(dataDir, engine, appendLog, 5*time.Minute)

    // Step 1: Load latest snapshot
    snapshot, err := snapshotMgr.LoadLatest()
    if err != nil && err != ErrNoSnapshot {
        return err
    }

    var startSeq uint64
    if snapshot != nil {
        log.Printf("Loaded snapshot at sequence %d", snapshot.SequenceNumber)
        if err := snapshotMgr.RestoreFromSnapshot(snapshot); err != nil {
            return err
        }
        startSeq = snapshot.SequenceNumber + 1
    }

    // Step 2: Replay log entries after snapshot
    reader, err := appendLog.Read(startSeq)
    if err != nil && err != io.EOF {
        return err
    }

    entriesReplayed := 0
    for {
        entry, err := reader.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("corrupted log entry: %w", err)
        }

        // Apply entry based on type
        switch entry.Type {
        case proto.LogEntryType_CRDT_DELTA:
            var delta crdt.Delta
            // Unmarshal and apply delta
            if err := engine.ApplyDelta(entry.CrdtId, delta); err != nil {
                return err
            }
            entriesReplayed++

        case proto.LogEntryType_MEMBERSHIP_CHANGE:
            // Handle membership changes
        }
    }

    log.Printf("Recovery complete: replayed %d log entries", entriesReplayed)
    return nil
}
```

### Testing

**Create `pkg/persistence/log_test.go`**:
```go
func TestAppendLogBasic(t *testing.T) {
    tmpDir := t.TempDir()
    logPath := filepath.Join(tmpDir, "test.log")

    log, err := NewAppendLog(logPath, 1<<20)
    assert.NoError(t, err)
    defer log.Close()

    // Append entries
    for i := 0; i < 100; i++ {
        entry := &proto.LogEntry{
            Type:    proto.LogEntryType_CRDT_DELTA,
            Payload: []byte(fmt.Sprintf("entry-%d", i)),
        }
        err := log.Append(entry)
        assert.NoError(t, err)
    }

    // Read back
    reader, err := log.Read(1)
    assert.NoError(t, err)

    count := 0
    for {
        _, err := reader.Next()
        if err == io.EOF {
            break
        }
        assert.NoError(t, err)
        count++
    }

    assert.Equal(t, 100, count)
}

func TestLogChecksum(t *testing.T) {
    // Test that corrupted entries are detected
}

func TestLogRotation(t *testing.T) {
    // Test log rotation at max size
}
```

**Create `pkg/persistence/snapshot_test.go`**:
```go
func TestSnapshotRestore(t *testing.T) {
    tmpDir := t.TempDir()

    // Create engine with some CRDTs
    engine := storage.NewEngine("node1")
    counter, _ := engine.GetOrCreate("counter1", storage.TypeGCounter)
    gc := counter.(*crdt.GCounter)
    gc.Increment(42)

    // Take snapshot
    log, _ := NewAppendLog(filepath.Join(tmpDir, "log"), 1<<20)
    snapshotMgr := NewSnapshotManager(tmpDir, engine, log, time.Minute)
    err := snapshotMgr.TakeSnapshot()
    assert.NoError(t, err)

    // Create new engine and restore
    engine2 := storage.NewEngine("node1")
    snapshot, err := snapshotMgr.LoadLatest()
    assert.NoError(t, err)

    err = snapshotMgr.RestoreFromSnapshot(snapshot)
    assert.NoError(t, err)

    // Verify restored state
    counter2, _ := engine2.GetOrCreate("counter1", storage.TypeGCounter)
    gc2 := counter2.(*crdt.GCounter)
    assert.Equal(t, uint64(42), gc2.Value())
}

func TestRecoveryFlow(t *testing.T) {
    // Test full recovery: snapshot + log replay
}
```

**Integration Test**:
```go
func TestPersistenceIntegration(t *testing.T) {
    tmpDir := t.TempDir()

    // Scenario: Write operations, take snapshot, write more, crash, recover

    // Phase 1: Initial operations
    engine := storage.NewEngine("node1")
    log, _ := NewAppendLog(filepath.Join(tmpDir, "log"), 1<<20)

    counter, _ := engine.GetOrCreate("counter1", storage.TypeGCounter)
    gc := counter.(*crdt.GCounter)

    for i := 0; i < 50; i++ {
        delta := gc.Increment(1)
        log.Append(deltaToLogEntry(delta))
    }

    // Phase 2: Snapshot
    snapshotMgr := NewSnapshotManager(tmpDir, engine, log, time.Minute)
    snapshotMgr.TakeSnapshot()

    // Phase 3: More operations
    for i := 0; i < 50; i++ {
        delta := gc.Increment(1)
        log.Append(deltaToLogEntry(delta))
    }

    // Phase 4: Simulate crash and recovery
    engine2 := storage.NewEngine("node1")
    err := RecoverNode(tmpDir, engine2)
    assert.NoError(t, err)

    // Verify final state
    counter2, _ := engine2.GetOrCreate("counter1", storage.TypeGCounter)
    gc2 := counter2.(*crdt.GCounter)
    assert.Equal(t, uint64(100), gc2.Value())
}
```

### Success Criteria
- [ ] Append-only log persists entries durably
- [ ] Checksum detects corrupted entries
- [ ] Snapshots capture full state
- [ ] Recovery correctly restores state (snapshot + log replay)
- [ ] Log rotation works at max size
- [ ] Performance: >5k log writes/sec with fsync

---

## Phase 4: gRPC Communication Layer (Week 6)

### Goal
Implement gRPC services for inter-node and client communication with mutual TLS.

### Tasks

#### 4.1 Protocol Buffer Service Definitions

**Create `api/proto/node_service.proto`**:
```protobuf
syntax = "proto3";

package node;
option go_package = "in-memorydb/api/proto;proto";

import "persistence.proto";

service NodeService {
  rpc Gossip(GossipMessage) returns (GossipAck);
  rpc SyncState(SyncRequest) returns (stream SyncResponse);
  rpc PushDelta(CRDTDeltaMessage) returns (DeltaAck);
  rpc Ping(PingRequest) returns (PingResponse);
}

message GossipMessage {
  repeated NodeState membership_updates = 1;
  repeated CRDTDeltaMessage crdt_deltas = 2;
  uint64 timestamp = 3;
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

message CRDTDeltaMessage {
  string crdt_id = 1;
  int32 crdt_type = 2;
  bytes delta_payload = 3;
  map<string, uint64> version = 4;
  uint64 timestamp = 5;
}

message GossipAck {
  bool success = 1;
  uint64 timestamp = 2;
}

message SyncRequest {
  map<string, VectorClockProto> local_versions = 1;
}

message SyncResponse {
  string crdt_id = 1;
  bytes full_state = 2;
  repeated CRDTDeltaMessage deltas = 3;
}

message PingRequest {
  string node_id = 1;
}

message PingResponse {
  string node_id = 1;
  uint64 timestamp = 2;
}

message VectorClockProto {
  map<string, uint64> clock = 1;
}
```

**Create `api/proto/client_service.proto`**:
```protobuf
syntax = "proto3";

package client;
option go_package = "in-memorydb/api/proto;proto";

service ClientService {
  // Counter operations
  rpc IncrementCounter(IncrementRequest) returns (IncrementResponse);
  rpc DecrementCounter(DecrementRequest) returns (DecrementResponse);
  rpc GetCounter(GetRequest) returns (CounterResponse);

  // Register operations
  rpc SetRegister(SetRequest) returns (SetResponse);
  rpc GetRegister(GetRequest) returns (RegisterResponse);

  // Cluster info
  rpc GetClusterStatus(StatusRequest) returns (StatusResponse);
}

message IncrementRequest {
  string key = 1;
  uint64 delta = 2;
}

message IncrementResponse {
  bool success = 1;
  uint64 new_value = 2;
}

message DecrementRequest {
  string key = 1;
  uint64 delta = 2;
}

message DecrementResponse {
  bool success = 1;
  int64 new_value = 2;
}

message GetRequest {
  string key = 1;
}

message CounterResponse {
  uint64 value = 1;
}

message SetRequest {
  string key = 1;
  bytes value = 2;
}

message SetResponse {
  bool success = 1;
}

message RegisterResponse {
  bytes value = 1;
  repeated bytes concurrent_values = 2;  // For MV-Register
}

message StatusRequest {}

message StatusResponse {
  int32 cluster_size = 1;
  int32 reachable_nodes = 2;
  repeated string node_ids = 3;
  string status = 4;
}
```

**Generate**:
```bash
make proto
```

#### 4.2 TLS Certificate Management

**Create script `scripts/gen-certs.sh`**:
```bash
#!/bin/bash

# Generate CA
openssl genrsa -out ca.key 4096
openssl req -new -x509 -key ca.key -out ca.crt -days 3650 \
    -subj "/C=US/ST=State/L=City/O=InMemoryDB/CN=InMemoryDB CA"

# Generate node certificates
for i in {1..5}; do
    NODE_ID="node-$i"

    # Generate private key
    openssl genrsa -out "${NODE_ID}.key" 4096

    # Generate CSR
    openssl req -new -key "${NODE_ID}.key" -out "${NODE_ID}.csr" \
        -subj "/C=US/ST=State/L=City/O=InMemoryDB/CN=${NODE_ID}"

    # Sign certificate
    openssl x509 -req -in "${NODE_ID}.csr" -CA ca.crt -CAkey ca.key \
        -CAcreateserial -out "${NODE_ID}.crt" -days 365

    rm "${NODE_ID}.csr"
done

echo "Certificates generated successfully"
```

**Create `pkg/crypto/tls.go`**:
```go
package crypto

import (
    "crypto/tls"
    "crypto/x509"
    "os"
)

func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
    // Load node certificate
    cert, err := tls.LoadX509KeyPair(certFile, keyFile)
    if err != nil {
        return nil, err
    }

    // Load CA certificate
    caCert, err := os.ReadFile(caFile)
    if err != nil {
        return nil, err
    }

    caCertPool := x509.NewCertPool()
    if !caCertPool.AppendCertsFromPEM(caCert) {
        return nil, fmt.Errorf("failed to parse CA certificate")
    }

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        ClientCAs:    caCertPool,
        ClientAuth:   tls.RequireAndVerifyClientCert,
        MinVersion:   tls.VersionTLS13,
        CipherSuites: []uint16{
            tls.TLS_AES_256_GCM_SHA384,
            tls.TLS_CHACHA20_POLY1305_SHA256,
        },
    }, nil
}
```

#### 4.3 Node Service Implementation

**Create `pkg/grpc/node_service.go`**:
```go
package grpc

import (
    "context"
    "in-memorydb/api/proto"
    "in-memorydb/pkg/gossip"
    "in-memorydb/pkg/storage"
)

type NodeServer struct {
    proto.UnimplementedNodeServiceServer
    nodeID    string
    gossip    *gossip.GossipLayer
    engine    *storage.Engine
}

func NewNodeServer(nodeID string, gossipLayer *gossip.GossipLayer, engine *storage.Engine) *NodeServer {
    return &NodeServer{
        nodeID: nodeID,
        gossip: gossipLayer,
        engine: engine,
    }
}

func (s *NodeServer) Gossip(ctx context.Context, msg *proto.GossipMessage) (*proto.GossipAck, error) {
    // Process membership updates
    for _, nodeState := range msg.MembershipUpdates {
        s.gossip.ProcessMembershipUpdate(nodeState)
    }

    // Process CRDT deltas
    for _, deltaMsg := range msg.CrdtDeltas {
        delta := s.unmarshalDelta(deltaMsg)
        if err := s.engine.ApplyDelta(deltaMsg.CrdtId, delta); err != nil {
            log.Printf("Failed to apply delta: %v", err)
        }
    }

    return &proto.GossipAck{
        Success:   true,
        Timestamp: uint64(time.Now().Unix()),
    }, nil
}

func (s *NodeServer) SyncState(req *proto.SyncRequest, stream proto.NodeService_SyncStateServer) error {
    // Anti-entropy: find divergent keys
    divergentKeys := s.engine.GetDivergentKeys(req.LocalVersions)

    for _, key := range divergentKeys {
        entry, err := s.engine.GetEntry(key)
        if err != nil {
            continue
        }

        // Send full state
        stateBytes, _ := entry.CRDT.Marshal()

        response := &proto.SyncResponse{
            CrdtId:    key,
            FullState: stateBytes,
        }

        if err := stream.Send(response); err != nil {
            return err
        }
    }

    return nil
}

func (s *NodeServer) PushDelta(ctx context.Context, delta *proto.CRDTDeltaMessage) (*proto.DeltaAck, error) {
    // Fast path for delta propagation
    d := s.unmarshalDelta(delta)
    err := s.engine.ApplyDelta(delta.CrdtId, d)

    return &proto.DeltaAck{
        Success: err == nil,
    }, err
}

func (s *NodeServer) Ping(ctx context.Context, req *proto.PingRequest) (*proto.PingResponse, error) {
    return &proto.PingResponse{
        NodeId:    s.nodeID,
        Timestamp: uint64(time.Now().Unix()),
    }, nil
}
```

#### 4.4 Client Service Implementation

**Create `pkg/grpc/client_service.go`**:
```go
package grpc

import (
    "context"
    "in-memorydb/api/proto"
    "in-memorydb/pkg/storage"
    "in-memorydb/pkg/crdt"
)

type ClientServer struct {
    proto.UnimplementedClientServiceServer
    engine *storage.Engine
}

func NewClientServer(engine *storage.Engine) *ClientServer {
    return &ClientServer{engine: engine}
}

func (s *ClientServer) IncrementCounter(ctx context.Context, req *proto.IncrementRequest) (*proto.IncrementResponse, error) {
    counter, err := s.engine.GetOrCreate(req.Key, storage.TypeGCounter)
    if err != nil {
        return nil, err
    }

    gc := counter.(*crdt.GCounter)
    gc.Increment(req.Delta)

    return &proto.IncrementResponse{
        Success:  true,
        NewValue: gc.Value(),
    }, nil
}

func (s *ClientServer) DecrementCounter(ctx context.Context, req *proto.DecrementRequest) (*proto.DecrementResponse, error) {
    counter, err := s.engine.GetOrCreate(req.Key, storage.TypePNCounter)
    if err != nil {
        return nil, err
    }

    pn := counter.(*crdt.PNCounter)
    pn.Decrement(req.Delta)

    return &proto.DecrementResponse{
        Success:  true,
        NewValue: pn.Value(),
    }, nil
}

func (s *ClientServer) GetCounter(ctx context.Context, req *proto.GetRequest) (*proto.CounterResponse, error) {
    entry, err := s.engine.GetEntry(req.Key)
    if err != nil {
        return nil, err
    }

    var value uint64
    switch c := entry.CRDT.(type) {
    case *crdt.GCounter:
        value = c.Value()
    case *crdt.PNCounter:
        value = uint64(c.Value())
    default:
        return nil, fmt.Errorf("not a counter type")
    }

    return &proto.CounterResponse{Value: value}, nil
}

func (s *ClientServer) SetRegister(ctx context.Context, req *proto.SetRequest) (*proto.SetResponse, error) {
    register, err := s.engine.GetOrCreate(req.Key, storage.TypeLWWRegister)
    if err != nil {
        return nil, err
    }

    lww := register.(*crdt.LWWRegister)
    lww.Set(req.Value)

    return &proto.SetResponse{Success: true}, nil
}

func (s *ClientServer) GetRegister(ctx context.Context, req *proto.GetRequest) (*proto.RegisterResponse, error) {
    entry, err := s.engine.GetEntry(req.Key)
    if err != nil {
        return nil, err
    }

    switch reg := entry.CRDT.(type) {
    case *crdt.LWWRegister:
        value := reg.Get()
        return &proto.RegisterResponse{Value: value.([]byte)}, nil

    case *crdt.MVRegister:
        values := reg.Get()
        response := &proto.RegisterResponse{
            ConcurrentValues: make([][]byte, len(values)),
        }
        for i, v := range values {
            response.ConcurrentValues[i] = v.([]byte)
        }
        return response, nil

    default:
        return nil, fmt.Errorf("not a register type")
    }
}

func (s *ClientServer) GetClusterStatus(ctx context.Context, req *proto.StatusRequest) (*proto.StatusResponse, error) {
    // Implementation depends on gossip membership
    return &proto.StatusResponse{
        ClusterSize:    int32(s.getClusterSize()),
        ReachableNodes: int32(s.getReachableNodes()),
        Status:         "healthy",
    }, nil
}
```

#### 4.5 gRPC Server Setup

**Create `pkg/grpc/server.go`**:
```go
package grpc

import (
    "context"
    "fmt"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "in-memorydb/api/proto"
    "in-memorydb/pkg/crypto"
    "net"
)

type Server struct {
    nodeServer   *NodeServer
    clientServer *ClientServer
    grpcServer   *grpc.Server
    listener     net.Listener
}

func NewServer(nodeAddr, clientAddr string, tlsConfig *tls.Config, nodeServer *NodeServer, clientServer *ClientServer) (*Server, error) {
    creds := credentials.NewTLS(tlsConfig)

    grpcServer := grpc.NewServer(
        grpc.Creds(creds),
        grpc.MaxRecvMsgSize(10 * 1024 * 1024), // 10MB
        grpc.MaxSendMsgSize(10 * 1024 * 1024),
    )

    // Register services
    proto.RegisterNodeServiceServer(grpcServer, nodeServer)
    proto.RegisterClientServiceServer(grpcServer, clientServer)

    // Create listener
    listener, err := net.Listen("tcp", nodeAddr)
    if err != nil {
        return nil, err
    }

    return &Server{
        nodeServer:   nodeServer,
        clientServer: clientServer,
        grpcServer:   grpcServer,
        listener:     listener,
    }, nil
}

func (s *Server) Start() error {
    log.Printf("Starting gRPC server on %s", s.listener.Addr())
    return s.grpcServer.Serve(s.listener)
}

func (s *Server) Stop() {
    s.grpcServer.GracefulStop()
}
```

### Testing

**Create `pkg/grpc/node_service_test.go`**:
```go
func TestGossipRPC(t *testing.T) {
    // Setup test server
    engine := storage.NewEngine("node1")
    gossipLayer := gossip.NewGossipLayer("node1")
    server := NewNodeServer("node1", gossipLayer, engine)

    // Create gossip message with deltas
    msg := &proto.GossipMessage{
        CrdtDeltas: []*proto.CRDTDeltaMessage{
            {
                CrdtId:   "counter1",
                CrdtType: int32(storage.TypeGCounter),
                // ... delta payload
            },
        },
    }

    // Call Gossip RPC
    ack, err := server.Gossip(context.Background(), msg)
    assert.NoError(t, err)
    assert.True(t, ack.Success)

    // Verify delta was applied
    counter, _ := engine.GetOrCreate("counter1", storage.TypeGCounter)
    // ... assertions
}

func TestAntiEntropySync(t *testing.T) {
    // Test SyncState RPC
}
```

**Integration Test with TLS**:
```go
func TestGRPCWithTLS(t *testing.T) {
    // Generate test certificates
    // Start server with TLS
    // Create client with TLS
    // Test RPC calls
}
```

### Success Criteria
- [ ] gRPC services implement all proto definitions
- [ ] mTLS authentication works correctly
- [ ] Node-to-node communication functional
- [ ] Client API operations work
- [ ] Performance: <5ms RPC latency on localhost

---

## Phase 5: Membership and Gossip Protocol (Week 7-8)

### Goal
Implement SWIM-inspired membership management and gossip-based CRDT propagation.

### Tasks

#### 5.1 Membership Manager

**Create `pkg/membership/member.go`**:
```go
package membership

import (
    "sync"
    "time"
)

type MembershipManager struct {
    mu            sync.RWMutex
    localNodeID   string
    members       map[string]*Member
    incarnation   uint64
    failureTimeout time.Duration
}

type Member struct {
    NodeID      string
    Address     string
    Status      NodeStatus
    Incarnation uint64
    LastSeen    time.Time
    State       MemberState
}

type NodeStatus int
const (
    StatusAlive NodeStatus = iota
    StatusSuspected
    StatusDead
    StatusLeft
)

type MemberState struct {
    mu               sync.RWMutex
    suspectTimer     *time.Timer
    confirmedByCount int
}

func NewMembershipManager(nodeID string, address string, failureTimeout time.Duration) *MembershipManager {
    mgr := &MembershipManager{
        localNodeID:    nodeID,
        members:        make(map[string]*Member),
        incarnation:    0,
        failureTimeout: failureTimeout,
    }

    // Add self as alive
    mgr.members[nodeID] = &Member{
        NodeID:      nodeID,
        Address:     address,
        Status:      StatusAlive,
        Incarnation: 0,
        LastSeen:    time.Now(),
    }

    return mgr
}

func (m *MembershipManager) AddMember(nodeID, address string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if _, exists := m.members[nodeID]; !exists {
        m.members[nodeID] = &Member{
            NodeID:      nodeID,
            Address:     address,
            Status:      StatusAlive,
            Incarnation: 0,
            LastSeen:    time.Now(),
        }
    }
}

func (m *MembershipManager) UpdateMemberStatus(nodeID string, status NodeStatus, incarnation uint64) bool {
    m.mu.Lock()
    defer m.mu.Unlock()

    member, exists := m.members[nodeID]
    if !exists {
        return false
    }

    // Only update if incarnation is newer or equal
    if incarnation < member.Incarnation {
        return false
    }

    // Special handling for suspected status
    if status == StatusSuspected && member.Status == StatusAlive {
        member.Status = StatusSuspected
        member.LastSeen = time.Now()

        // Start timer to confirm as dead
        member.State.suspectTimer = time.AfterFunc(m.failureTimeout, func() {
            m.ConfirmDead(nodeID)
        })

        return true
    }

    if status == StatusDead {
        member.Status = StatusDead
        member.LastSeen = time.Now()
        return true
    }

    // Node refutes suspicion with higher incarnation
    if nodeID == m.localNodeID && status == StatusAlive && incarnation > member.Incarnation {
        member.Status = StatusAlive
        member.Incarnation = incarnation
        if member.State.suspectTimer != nil {
            member.State.suspectTimer.Stop()
        }
        return true
    }

    member.Status = status
    member.Incarnation = incarnation
    member.LastSeen = time.Now()

    return true
}

func (m *MembershipManager) ConfirmDead(nodeID string) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if member, exists := m.members[nodeID]; exists {
        if member.Status == StatusSuspected {
            member.Status = StatusDead
            log.Printf("Node %s confirmed as dead", nodeID)
        }
    }
}

func (m *MembershipManager) GetAliveMembers() []*Member {
    m.mu.RLock()
    defer m.mu.RUnlock()

    var alive []*Member
    for _, member := range m.members {
        if member.Status == StatusAlive {
            alive = append(alive, member)
        }
    }
    return alive
}

func (m *MembershipManager) SelectRandomMembers(count int) []*Member {
    alive := m.GetAliveMembers()

    if len(alive) <= count {
        return alive
    }

    // Random selection
    rand.Shuffle(len(alive), func(i, j int) {
        alive[i], alive[j] = alive[j], alive[i]
    })

    return alive[:count]
}

func (m *MembershipManager) IncarnateLocal() uint64 {
    m.mu.Lock()
    defer m.mu.Unlock()

    m.incarnation++
    if member, exists := m.members[m.localNodeID]; exists {
        member.Incarnation = m.incarnation
    }

    return m.incarnation
}
```

#### 5.2 Gossip Layer

**Create `pkg/gossip/gossip.go`**:
```go
package gossip

import (
    "context"
    "in-memorydb/api/proto"
    "in-memorydb/pkg/membership"
    "in-memorydb/pkg/storage"
    "time"
)

type GossipLayer struct {
    nodeID         string
    membership     *membershipMembershipManager
    engine         *storage.Engine
    deltaBuffer    *storage.DeltaBuffer
    interval       time.Duration
    fanout         int
    nodeClient     NodeClientPool
    antiEntropyInt time.Duration
}

type NodeClientPool struct {
    mu      sync.RWMutex
    clients map[string]proto.NodeServiceClient
    dialOptions []grpc.DialOption
}

func NewGossipLayer(nodeID string, membership *membership.MembershipManager, engine *storage.Engine, config GossipConfig) *GossipLayer {
    return &GossipLayer{
        nodeID:         nodeID,
        membership:     membership,
        engine:         engine,
        deltaBuffer:    storage.NewDeltaBuffer(100),
        interval:       config.Interval,
        fanout:         config.Fanout,
        antiEntropyInt: config.AntiEntropyInterval,
    }
}

func (g *GossipLayer) Start(ctx context.Context) {
    // Start membership gossip
    go g.membershipGossipLoop(ctx)

    // Start anti-entropy
    go g.antiEntropyLoop(ctx)
}

func (g *GossipLayer) membershipGossipLoop(ctx context.Context) {
    ticker := time.NewTicker(g.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            g.doGossipRound()
        case <-ctx.Done():
            return
        }
    }
}

func (g *GossipLayer) doGossipRound() {
    // Select random targets
    targets := g.membership.SelectRandomMembers(g.fanout)

    // Build gossip message
    msg := &proto.GossipMessage{
        MembershipUpdates: g.getMembershipUpdates(),
        CrdtDeltas:        g.getPendingDeltas(),
        Timestamp:         uint64(time.Now().Unix()),
    }

    // Send to each target
    for _, target := range targets {
        if target.NodeID == g.nodeID {
            continue
        }

        go func(targetMember *membership.Member) {
            client, err := g.nodeClient.GetClient(targetMember.Address)
            if err != nil {
                log.Printf("Failed to get client for %s: %v", targetMember.NodeID, err)
                g.handleGossipFailure(targetMember)
                return
            }

            ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
            defer cancel()

            ack, err := client.Gossip(ctx, msg)
            if err != nil {
                log.Printf("Gossip to %s failed: %v", targetMember.NodeID, err)
                g.handleGossipFailure(targetMember)
                return
            }

            // Mark deltas as acknowledged
            g.deltaBuffer.MarkAcked(targetMember.NodeID, ack.Timestamp)
        }(target)
    }
}

func (g *GossipLayer) getMembershipUpdates() []*proto.NodeState {
    members := g.membership.GetAliveMembers()

    updates := make([]*proto.NodeState, 0, len(members))
    for _, member := range members {
        updates = append(updates, &proto.NodeState{
            NodeId:      member.NodeID,
            Address:     member.Address,
            Status:      proto.NodeStatus(member.Status),
            Incarnation: member.Incarnation,
            Timestamp:   uint64(member.LastSeen.Unix()),
        })
    }

    return updates
}

func (g *GossipLayer) getPendingDeltas() []*proto.CRDTDeltaMessage {
    // Get deltas from buffer (implementation depends on target)
    return []*proto.CRDTDeltaMessage{}
}

func (g *GossipLayer) handleGossipFailure(member *membership.Member) {
    // Mark as suspected after consecutive failures
    g.membership.UpdateMemberStatus(member.NodeID, membership.StatusSuspected, member.Incarnation)
}

func (g *GossipLayer) ProcessMembershipUpdate(nodeState *proto.NodeState) {
    g.membership.UpdateMemberStatus(
        nodeState.NodeId,
        membership.NodeStatus(nodeState.Status),
        nodeState.Incarnation,
    )
}
```

#### 5.3 Anti-Entropy

**Create `pkg/gossip/anti_entropy.go`**:
```go
package gossip

import (
    "context"
    "in-memorydb/api/proto"
    "time"
)

func (g *GossipLayer) antiEntropyLoop(ctx context.Context) {
    ticker := time.NewTicker(g.antiEntropyInt)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            g.doAntiEntropyRound()
        case <-ctx.Done():
            return
        }
    }
}

func (g *GossipLayer) doAntiEntropyRound() {
    // Select one random peer
    targets := g.membership.SelectRandomMembers(1)
    if len(targets) == 0 {
        return
    }

    target := targets[0]
    if target.NodeID == g.nodeID {
        return
    }

    log.Printf("Starting anti-entropy with %s", target.NodeID)

    // Get local versions
    localVersions := g.engine.GetAllVersions()

    // Send sync request
    client, err := g.nodeClient.GetClient(target.Address)
    if err != nil {
        return
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    req := &proto.SyncRequest{
        LocalVersions: convertVersions(localVersions),
    }

    stream, err := client.SyncState(ctx, req)
    if err != nil {
        log.Printf("Anti-entropy failed: %v", err)
        return
    }

    // Receive and apply updates
    updatesApplied := 0
    for {
        resp, err := stream.Recv()
        if err == io.EOF {
            break
        }
        if err != nil {
            log.Printf("Anti-entropy stream error: %v", err)
            return
        }

        // Apply full state or deltas
        if len(resp.FullState) > 0 {
            // Unmarshal and merge full state
            g.applyFullState(resp.CrdtId, resp.FullState)
            updatesApplied++
        }

        for _, deltaMsg := range resp.Deltas {
            delta := unmarshalDelta(deltaMsg)
            g.engine.ApplyDelta(deltaMsg.CrdtId, delta)
            updatesApplied++
        }
    }

    log.Printf("Anti-entropy complete: applied %d updates from %s", updatesApplied, target.NodeID)
}

func (g *GossipLayer) applyFullState(crdtID string, stateBytes []byte) error {
    // Get or create CRDT
    // Unmarshal remote state
    // Merge with local state
    return nil
}
```

### Testing

**Create `pkg/gossip/gossip_test.go`**:
```go
func TestMembershipGossip(t *testing.T) {
    // Create 3-node cluster
    // Start gossip
    // Verify membership propagates
}

func TestNodeFailureDetection(t *testing.T) {
    // Start 3 nodes
    // Kill one node
    // Verify others detect failure
    // Verify status changes: Alive -> Suspected -> Dead
}

func TestAntiEntropy(t *testing.T) {
    // Create two nodes with divergent state
    // Run anti-entropy
    // Verify convergence
}
```

**Integration Test** (`test/integration/cluster_test.go`):
```go
func TestClusterFormation(t *testing.T) {
    // Start 5 nodes
    // Verify all nodes discover each other
    // Check membership consistency
}

func TestGossipPropagation(t *testing.T) {
    // Start 3 nodes
    // Write to node 1
    // Wait for gossip
    // Verify all nodes have the update
}
```

### Success Criteria
- [ ] Membership gossip propagates within 3 rounds
- [ ] Node failures detected within 10 seconds
- [ ] Anti-entropy achieves convergence
- [ ] No split-brain scenarios
- [ ] Performance: Handle 100-node cluster

---

## Phase 6: Main Node Implementation and CLI (Week 9)

### Goal
Bring everything together into a runnable node and create client CLI.

### Tasks

#### 6.1 Node Main

**Create `cmd/node/main.go`**:
```go
package main

import (
    "context"
    "flag"
    "log"
    "os"
    "os/signal"
    "syscall"

    "in-memorydb/pkg/config"
    "in-memorydb/pkg/storage"
    "in-memorydb/pkg/persistence"
    "in-memorydb/pkg/membership"
    "in-memorydb/pkg/gossip"
    "in-memorydb/pkg/grpc"
    "in-memorydb/pkg/crypto"
)

func main() {
    configPath := flag.String("config", "config.yaml", "Path to configuration file")
    flag.Parse()

    // Load configuration
    cfg, err := config.LoadConfig(*configPath)
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    // Initialize logger
    logger := util.NewLogger(cfg.Node.ID)

    // Initialize storage engine
    engine := storage.NewEngine(cfg.Node.ID)

    // Initialize persistence
    os.MkdirAll(cfg.Node.DataDir, 0755)

    appendLog, err := persistence.NewAppendLog(
        filepath.Join(cfg.Node.DataDir, "log"),
        cfg.Persistence.LogMaxSizeMB * 1024 * 1024,
    )
    if err != nil {
        log.Fatalf("Failed to create append log: %v", err)
    }

    snapshotMgr := persistence.NewSnapshotManager(
        cfg.Node.DataDir,
        engine,
        appendLog,
        time.Duration(cfg.Persistence.SnapshotIntervalSec)*time.Second,
    )

    // Recovery
    if err := persistence.RecoverNode(cfg.Node.DataDir, engine); err != nil {
        log.Fatalf("Recovery failed: %v", err)
    }

    // Initialize membership
    membershipMgr := membership.NewMembershipManager(
        cfg.Node.ID,
        cfg.Node.AdvertiseAddress,
        time.Duration(cfg.Gossip.FailureTimeoutMs)*time.Millisecond,
    )

    // Join existing cluster if seed nodes provided
    for _, seed := range cfg.Cluster.SeedNodes {
        membershipMgr.AddMember(seed.NodeID, seed.Address)
    }

    // Initialize gossip
    gossipConfig := gossip.GossipConfig{
        Interval:            time.Duration(cfg.Gossip.IntervalMs) * time.Millisecond,
        Fanout:              cfg.Gossip.Fanout,
        AntiEntropyInterval: 60 * time.Second,
    }

    gossipLayer := gossip.NewGossipLayer(cfg.Node.ID, membershipMgr, engine, gossipConfig)

    // Initialize TLS
    tlsConfig, err := crypto.LoadTLSConfig(
        cfg.Security.CertFile,
        cfg.Security.KeyFile,
        cfg.Security.CaFile,
    )
    if err != nil {
        log.Fatalf("Failed to load TLS config: %v", err)
    }

    // Initialize gRPC servers
    nodeServer := grpc.NewNodeServer(cfg.Node.ID, gossipLayer, engine)
    clientServer := grpc.NewClientServer(engine)

    grpcServer, err := grpc.NewServer(
        cfg.Node.BindAddress,
        cfg.ClientAPI.BindAddress,
        tlsConfig,
        nodeServer,
        clientServer,
    )
    if err != nil {
        log.Fatalf("Failed to create gRPC server: %v", err)
    }

    // Start components
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go gossipLayer.Start(ctx)
    go snapshotMgr.Start(ctx)

    // Start gRPC server
    go func() {
        if err := grpcServer.Start(); err != nil {
            log.Fatalf("gRPC server failed: %v", err)
        }
    }()

    logger.Info("Node started successfully")

    // Wait for shutdown signal
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
    <-sigChan

    logger.Info("Shutting down...")

    // Graceful shutdown
    cancel()
    grpcServer.Stop()
    snapshotMgr.TakeSnapshot()  // Final snapshot
    appendLog.Close()

    logger.Info("Shutdown complete")
}
```

#### 6.2 Client CLI

**Create `cmd/client/main.go`**:
```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "in-memorydb/api/proto"
    "in-memorydb/pkg/crypto"
)

func main() {
    serverAddr := flag.String("server", "localhost:8080", "Server address")
    certFile := flag.String("cert", "client.crt", "Client certificate")
    keyFile := flag.String("key", "client.key", "Client key")
    caFile := flag.String("ca", "ca.crt", "CA certificate")

    flag.Parse()

    if len(flag.Args()) < 1 {
        printUsage()
        return
    }

    // Setup TLS
    tlsConfig, err := crypto.LoadTLSConfig(*certFile, *keyFile, *caFile)
    if err != nil {
        log.Fatalf("Failed to load TLS: %v", err)
    }

    creds := credentials.NewTLS(tlsConfig)

    // Connect
    conn, err := grpc.Dial(*serverAddr, grpc.WithTransportCredentials(creds))
    if err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }
    defer conn.Close()

    client := proto.NewClientServiceClient(conn)

    // Parse command
    command := flag.Args()[0]

    switch command {
    case "incr":
        if len(flag.Args()) < 2 {
            fmt.Println("Usage: incr <key> [delta]")
            return
        }
        key := flag.Args()[1]
        delta := uint64(1)
        if len(flag.Args()) > 2 {
            fmt.Sscanf(flag.Args()[2], "%d", &delta)
        }

        resp, err := client.IncrementCounter(context.Background(), &proto.IncrementRequest{
            Key:   key,
            Delta: delta,
        })
        if err != nil {
            log.Fatalf("Increment failed: %v", err)
        }

        fmt.Printf("OK: %d\n", resp.NewValue)

    case "get":
        if len(flag.Args()) < 2 {
            fmt.Println("Usage: get <key>")
            return
        }
        key := flag.Args()[1]

        resp, err := client.GetCounter(context.Background(), &proto.GetRequest{Key: key})
        if err != nil {
            log.Fatalf("Get failed: %v", err)
        }

        fmt.Printf("%d\n", resp.Value)

    case "set":
        if len(flag.Args()) < 3 {
            fmt.Println("Usage: set <key> <value>")
            return
        }
        key := flag.Args()[1]
        value := flag.Args()[2]

        resp, err := client.SetRegister(context.Background(), &proto.SetRequest{
            Key:   key,
            Value: []byte(value),
        })
        if err != nil {
            log.Fatalf("Set failed: %v", err)
        }

        if resp.Success {
            fmt.Println("OK")
        }

    case "status":
        resp, err := client.GetClusterStatus(context.Background(), &proto.StatusRequest{})
        if err != nil {
            log.Fatalf("Status failed: %v", err)
        }

        fmt.Printf("Cluster Status: %s\n", resp.Status)
        fmt.Printf("Cluster Size: %d nodes\n", resp.ClusterSize)
        fmt.Printf("Reachable: %d nodes\n", resp.ReachableNodes)

    default:
        fmt.Printf("Unknown command: %s\n", command)
        printUsage()
    }
}

func printUsage() {
    fmt.Println("Usage: client [options] <command> [args]")
    fmt.Println("\nCommands:")
    fmt.Println("  incr <key> [delta]  - Increment counter")
    fmt.Println("  decr <key> [delta]  - Decrement counter")
    fmt.Println("  get <key>           - Get counter value")
    fmt.Println("  set <key> <value>   - Set register value")
    fmt.Println("  status              - Get cluster status")
}
```

#### 6.3 Local Cluster Script

**Create `scripts/run-cluster.sh`**:
```bash
#!/bin/bash

NUM_NODES=${1:-3}
BASE_PORT=8080
CLIENT_BASE_PORT=9090

echo "Starting $NUM_NODES node cluster..."

# Kill any existing nodes
pkill -f "in-memorydb/bin/node" || true

# Generate configs
mkdir -p configs/cluster

for i in $(seq 1 $NUM_NODES); do
    NODE_ID="node-$i"
    NODE_PORT=$((BASE_PORT + i - 1))
    CLIENT_PORT=$((CLIENT_BASE_PORT + i - 1))
    DATA_DIR="data/node-$i"

    mkdir -p $DATA_DIR

    cat > configs/cluster/$NODE_ID.yaml <<EOF
node:
  id: "$NODE_ID"
  bind_address: "127.0.0.1:$NODE_PORT"
  advertise_address: "127.0.0.1:$NODE_PORT"
  data_dir: "$DATA_DIR"

gossip:
  interval_ms: 2000
  fanout: 3
  failure_timeout_ms: 10000
  suspected_timeout_ms: 6000

persistence:
  log_max_size_mb: 1024
  snapshot_interval_sec: 300
  log_retention_hours: 168

replication:
  mode: "full"

security:
  tls_enabled: true
  cert_file: "certs/$NODE_ID.crt"
  key_file: "certs/$NODE_ID.key"
  ca_file: "certs/ca.crt"

client_api:
  enabled: true
  bind_address: "127.0.0.1:$CLIENT_PORT"
  max_concurrent_requests: 1000

cluster:
  seed_nodes:
EOF

    # Add seed nodes (first node for others)
    if [ $i -gt 1 ]; then
        echo "    - node_id: \"node-1\"" >> configs/cluster/$NODE_ID.yaml
        echo "      address: \"127.0.0.1:8080\"" >> configs/cluster/$NODE_ID.yaml
    fi
done

# Start nodes
for i in $(seq 1 $NUM_NODES); do
    NODE_ID="node-$i"
    ./bin/node --config=configs/cluster/$NODE_ID.yaml > logs/$NODE_ID.log 2>&1 &
    echo "Started $NODE_ID (PID $!)"
    sleep 1
done

echo "Cluster started. Logs in logs/ directory"
echo "Connect with: ./bin/client -server localhost:9090 status"
```

### Testing

**Manual Testing**:
```bash
# Build
make build

# Generate certificates
cd certs && ../scripts/gen-certs.sh

# Start cluster
./scripts/run-cluster.sh 3

# Test operations
./bin/client -server localhost:9090 incr counter1 5
./bin/client -server localhost:9091 incr counter1 3
./bin/client -server localhost:9092 get counter1  # Should eventually be 8

# Check cluster status
./bin/client -server localhost:9090 status
```

### Success Criteria
- [ ] Nodes start successfully
- [ ] Cluster forms automatically
- [ ] Client can connect and execute operations
- [ ] Data propagates across nodes
- [ ] Nodes recover after restart

---

## Phase 7: Testing and Validation (Week 10)

### Goal
Comprehensive testing including chaos testing and validation of convergence guarantees.

### Tasks

#### 7.1 Integration Test Suite

**Create `test/integration/full_system_test.go`**:
```go
func TestFullSystemIntegration(t *testing.T) {
    // Start 5-node cluster
    // Perform concurrent writes from multiple clients
    // Verify eventual consistency
    // Test anti-entropy
}

func TestNetworkPartition(t *testing.T) {
    // Start 5 nodes
    // Partition into [1,2] and [3,4,5]
    // Write to both partitions
    // Heal partition
    // Verify convergence
}
```

#### 7.2 Chaos Testing

**Create `test/chaos/chaos_test.go`** using toxiproxy:
```go
func TestRandomNodeFailures(t *testing.T) {
    // Start cluster
    // Randomly kill and restart nodes
    // Verify data integrity throughout
}

func TestNetworkDelay(t *testing.T) {
    // Introduce variable network delays
    // Verify gossip still converges
}

func TestPacketLoss(t *testing.T) {
    // Simulate 10% packet loss
    // Verify anti-entropy compensates
}
```

#### 7.3 Property-Based Cluster Tests

Test CRDT convergence properties at cluster level:
```go
func TestClusterConvergenceProperty(t *testing.T) {
    // Generate random sequence of operations
    // Apply to random nodes with random timing
    // Introduce random failures
    // Verify all nodes eventually converge to same state
}
```

### Success Criteria
- [ ] All integration tests pass
- [ ] Chaos tests demonstrate resilience
- [ ] Convergence guaranteed within 30 seconds
- [ ] No data loss scenarios
- [ ] System recovers from all failure modes

---

## Phase 8: Documentation and Polish (Week 11)

### Goal
Complete documentation, performance tuning, and production readiness.

### Tasks

- Update CLAUDE.md with implementation details
- Add inline code documentation
- Performance benchmarking and optimization
- Metrics and observability (Prometheus integration)
- Admin API for operations (trigger snapshot, view membership, etc.)

---

## Timeline Summary

| Phase | Duration | Focus | Key Deliverables |
|-------|----------|-------|------------------|
| 0 | 1 week | Setup | Project structure, tooling |
| 1 | 2 weeks | CRDTs | G-Counter, PN-Counter, LWW-Register, MV-Register |
| 2 | 1 week | Storage | Storage engine, delta buffer |
| 3 | 1 week | Persistence | Append log, snapshots, recovery |
| 4 | 1 week | gRPC | Services, TLS, client API |
| 5 | 2 weeks | Gossip | Membership, gossip protocol, anti-entropy |
| 6 | 1 week | Integration | Node main, CLI client |
| 7 | 1 week | Testing | Integration, chaos tests |
| 8 | 1 week | Polish | Documentation, performance |

**Total**: ~11 weeks

## Development Best Practices

1. **Run tests after each change**: `make test`
2. **Use race detector**: `go test -race ./...`
3. **Benchmark critical paths**: `go test -bench=.`
4. **Review code coverage**: `go test -cover ./...`
5. **Profile performance issues**: `go test -cpuprofile=cpu.prof`
6. **Keep commits atomic**: One logical change per commit
7. **Write tests first**: TDD for critical components

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| CRDT bugs | Extensive property-based testing, formal verification reference |
| Network issues | Chaos testing, packet loss simulation |
| Data corruption | Checksums, snapshot validation |
| Performance bottlenecks | Early benchmarking, profiling |
| Race conditions | Always run with -race flag |
| State divergence | Comprehensive anti-entropy testing |

## Next Steps

Start with Phase 0 by creating the project structure and setting up the development environment. Once complete, proceed sequentially through each phase, ensuring all success criteria are met before moving forward.

After Phase 1 (CRDTs), you'll have a solid foundation to parallelize some work:
- One developer on persistence (Phase 3)
- Another on gRPC (Phase 4)
- Since both depend on storage (Phase 2)

Good luck with the implementation! Feel free to adapt this plan as you discover new requirements or constraints.