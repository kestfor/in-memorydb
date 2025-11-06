package crdt

import "github.com/google/uuid"

type CRDTFabric interface {
	New(name string, id uuid.UUID) (CRDT, error)
}

type CRDT interface {
	Snapshot() ([]byte, error)
	MergeSnapshot(other []byte) error
}

type CRDTConstructor func(id uuid.UUID) CRDT
