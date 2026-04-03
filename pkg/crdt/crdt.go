package crdt

//go:generate go-enum --marshal --nocase

//go:generate mockgen -source=crdt.go -destination=mocks/crdt.mock.go CRDT,Delta

// ENUM(PNCounter, LWWHLCRegister)
type CRDTType string

type CRDTFabric interface {
	// New returns ready to use crdt object
	New(crdtType CRDTType, id string) (CRDT, error)

	// NilDelta returns nil pointer casted to delta in order to use for Type() functions
	NilDelta(crdtType CRDTType) (Delta, error)

	// DeltaFromBytes creates delta from raw bytes
	DeltaFromBytes(crdtType CRDTType, bytes []byte) (Delta, error)
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

	Value() any

	// Get type of CRDT
	Type() CRDTType

	// Hash returns a fast, deterministic hash of the current CRDT state.
	// Used by anti-entropy to detect divergence without serialization.
	Hash() uint64
}

type Delta interface {
	// Serialize delta to bytes
	MarshalJSON() ([]byte, error)

	// Deserialize delta from bytes
	UnmarshalJSON(data []byte) error

	// Get type of crdt which delta belongs to, can be used on nil
	Type() CRDTType

	Merge(other Delta) error

	CreateCRDT() (CRDT, error)
}

type CRDTConstructor func(id string) CRDT
