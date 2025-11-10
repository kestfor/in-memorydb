package crdt

//go:generate go-enum --marshal --nocase

// ENUM(PNCounter, LWWHLCRegister)
type CRDTType string

type CRDTFabric interface {
	New(crdtType CRDTType, id string) (CRDT, error)
}

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
	Type() CRDTType
}

type Delta interface {
	// Serialize delta to bytes
	MarshalJSON() ([]byte, error)

	// Deserialize delta from bytes
	UnmarshalJSON(data []byte) error

	// Get type of crdt which delta belongs to
	Type() CRDTType

	Merge(other Delta) error
}

type CRDTConstructor func(id string) CRDT
