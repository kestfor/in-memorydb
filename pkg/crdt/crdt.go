package crdt

import (
	"github.com/google/uuid"
)

type CRDTFabric interface {
	New(name string, id uuid.UUID) (CRDT, error)
}

//type CRDT interface {
//	Snapshot() ([]byte, error)
//	MergeSnapshot(other []byte) error
//}

type CRDT interface {
	// Merge full state from another replica
	Merge(other CRDT) error

	// Apply a delta update
	ApplyDelta(delta Delta) error

	// Serialize state to bytes
	MarshalJSON() ([]byte, error)

	// Deserialize state from bytes
	UnmarshalJSON(data []byte) error

	// Get type of CRDT
	Type() string
}

type Delta interface {
	// Serialize delta to bytes
	MarshalJSON() ([]byte, error)

	// Deserialize delta from bytes
	UnmarshalJSON(data []byte) error

	Type() string
}

type CRDTConstructor func(id uuid.UUID) CRDT
